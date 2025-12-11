package wireguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"runtime"
	"strconv"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/conn"
)

// StdNetBind is a custom implementation of conn.Bind that sets SO_REUSEADDR and SO_REUSEPORT.
// This allows STUN discovery to share the same UDP port with the WireGuard device.
// Currently unused - reserved for future colony-based STUN implementation (see RFD 029).
type StdNetBind struct {
	mu     sync.Mutex // protects following fields
	ipv4   *net.UDPConn
	ipv6   *net.UDPConn
	ipv4PC *net.UDPConn // for PeekLookAtSocketFd
	ipv6PC *net.UDPConn // for PeekLookAtSocketFd

	// mu acquired and last return values
	blackhole4 bool
	blackhole6 bool
}

// NewStdNetBind creates a new StdNetBind.
func NewStdNetBind() conn.Bind {
	return &StdNetBind{}
}

type StdNetEndpoint struct {
	// AddrPort is the endpoint destination.
	netip.AddrPort
}

var (
	_ conn.Bind     = (*StdNetBind)(nil)
	_ conn.Endpoint = &StdNetEndpoint{}
)

func (*StdNetBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	e, err := netip.ParseAddrPort(s)
	if err != nil {
		return nil, err
	}
	return &StdNetEndpoint{
		AddrPort: e,
	}, nil
}

func (e *StdNetEndpoint) ClearSrc() {}

func (e *StdNetEndpoint) SrcToString() string { return "" }

func (e *StdNetEndpoint) DstToString() string { return e.String() }

func (e *StdNetEndpoint) DstToBytes() []byte {
	b, _ := e.MarshalBinary()
	return b
}

func (e *StdNetEndpoint) DstIP() netip.Addr {
	return e.Addr()
}

func (e *StdNetEndpoint) SrcIP() netip.Addr {
	return netip.Addr{}
}

func listenNet(network string, port int) (*net.UDPConn, int, error) {
	lc := &net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var sockoptErr error
			if err := c.Control(func(fd uintptr) {
				// Set SO_REUSEADDR to allow multiple sockets to bind to the same address.
				if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
					sockoptErr = err
					return
				}

				// Set SO_REUSEPORT to allow multiple sockets to bind to the same port.
				// Reserved for future colony-based STUN (RFD 029).
				if runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "freebsd" {
					if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1); err != nil {
						sockoptErr = err
						return
					}
				}
			}); err != nil {
				return err
			}
			return sockoptErr
		},
	}

	ctx := context.Background()
	conn, err := lc.ListenPacket(ctx, network, ":"+strconv.Itoa(port))
	if err != nil {
		return nil, 0, err
	}

	udpConn, ok := conn.(*net.UDPConn)
	if !ok {
		_ = conn.Close()
		return nil, 0, errors.New("failed to cast to UDPConn")
	}

	// Retrieve the actual port if 0 was specified
	localAddr := udpConn.LocalAddr().(*net.UDPAddr)
	return udpConn, localAddr.Port, nil
}

func (bind *StdNetBind) Open(uport uint16) ([]conn.ReceiveFunc, uint16, error) {
	bind.mu.Lock()
	defer bind.mu.Unlock()

	var err error
	var tries int

	if bind.ipv4 != nil || bind.ipv6 != nil {
		return nil, 0, conn.ErrBindAlreadyOpen
	}

	port := int(uport)

	for {
		var ipv4, ipv6 *net.UDPConn

		ipv4, port, err = listenNet("udp4", port)
		if err != nil && !errors.Is(err, syscall.EAFNOSUPPORT) {
			return nil, 0, err
		}

		// Only attempt ipv6 if the port was successfully assigned for ipv4
		// and if ipv6 is supported.
		if err == nil {
			ipv6, port, err = listenNet("udp6", port)
			if err != nil && !errors.Is(err, syscall.EAFNOSUPPORT) {
				_ = ipv4.Close() // TODO: errcheck
				return nil, 0, err
			}
		}

		// If uport was 0 (ephemeral), both ipv4 and ipv6 might have gotten
		// different ports. Close and retry until we get the same port.
		if uport == 0 && ipv4 != nil && ipv6 != nil {
			ipv4Addr := ipv4.LocalAddr().(*net.UDPAddr)
			ipv6Addr := ipv6.LocalAddr().(*net.UDPAddr)
			if ipv4Addr.Port != ipv6Addr.Port {
				_ = ipv4.Close() // TODO: errcheck
				_ = ipv6.Close() // TODO: errcheck
				tries++
				if tries >= 100 {
					return nil, 0, errors.New("failed to get matching ipv4 and ipv6 ports after 100 tries")
				}
				continue
			}
		}

		bind.ipv4 = ipv4
		bind.ipv6 = ipv6
		bind.ipv4PC = ipv4
		bind.ipv6PC = ipv6
		bind.blackhole4 = false
		bind.blackhole6 = false
		break
	}

	var fns []conn.ReceiveFunc
	if bind.ipv4 != nil {
		fns = append(fns, bind.receiveIPv4)
	}
	if bind.ipv6 != nil {
		fns = append(fns, bind.receiveIPv6)
	}
	if len(fns) == 0 {
		return nil, 0, errors.New("failed to bind to any address")
	}
	//nolint:gosec // G115: Port number is from validated socket address
	return fns, uint16(port), nil
}

func (bind *StdNetBind) receiveIPv4(packets [][]byte, sizes []int, eps []conn.Endpoint) (n int, err error) {
	bind.mu.Lock()
	defer bind.mu.Unlock()

	if bind.ipv4 == nil {
		return 0, net.ErrClosed
	}

	for i := range packets {
		if i >= len(sizes) || i >= len(eps) {
			break
		}
		var addrPort netip.AddrPort
		sizes[i], _, _, addrPort, err = bind.ipv4.ReadMsgUDPAddrPort(packets[i], nil)
		if err != nil {
			return i, err
		}
		eps[i] = &StdNetEndpoint{AddrPort: addrPort}
		n = i + 1
	}
	return n, nil
}

func (bind *StdNetBind) receiveIPv6(packets [][]byte, sizes []int, eps []conn.Endpoint) (n int, err error) {
	bind.mu.Lock()
	defer bind.mu.Unlock()

	if bind.ipv6 == nil {
		return 0, net.ErrClosed
	}

	for i := range packets {
		if i >= len(sizes) || i >= len(eps) {
			break
		}
		var addrPort netip.AddrPort
		sizes[i], _, _, addrPort, err = bind.ipv6.ReadMsgUDPAddrPort(packets[i], nil)
		if err != nil {
			return i, err
		}
		eps[i] = &StdNetEndpoint{AddrPort: addrPort}
		n = i + 1
	}
	return n, nil
}

func (bind *StdNetBind) Close() error {
	bind.mu.Lock()
	defer bind.mu.Unlock()

	var err1, err2 error
	if bind.ipv4 != nil {
		err1 = bind.ipv4.Close()
		bind.ipv4 = nil
	}
	if bind.ipv6 != nil {
		err2 = bind.ipv6.Close()
		bind.ipv6 = nil
	}
	bind.blackhole4 = false
	bind.blackhole6 = false
	if err1 != nil {
		return err1
	}
	return err2
}

func (bind *StdNetBind) Send(bufs [][]byte, endpoint conn.Endpoint) error {
	bind.mu.Lock()
	defer bind.mu.Unlock()

	nend, ok := endpoint.(*StdNetEndpoint)
	if !ok {
		return conn.ErrWrongEndpointType
	}

	addrPort := netip.AddrPort(nend.AddrPort)

	var c *net.UDPConn
	if addrPort.Addr().Is4() {
		c = bind.ipv4
	} else {
		c = bind.ipv6
	}

	if c == nil {
		return syscall.EAFNOSUPPORT
	}

	for _, buf := range bufs {
		_, err := c.WriteToUDPAddrPort(buf, addrPort)
		if err != nil {
			return err
		}
	}
	return nil
}

func (bind *StdNetBind) BatchSize() int {
	return 1
}

func (bind *StdNetBind) SetMark(mark uint32) error {
	// Mark setting is not supported on all platforms.
	// Return nil to indicate success (no-op).
	return nil
}
