package naming

import "testing"

func TestProcessNameAdaptor_Unique(t *testing.T) {
	a := NewProcessNameAdaptor()

	name, ok := a.Resolve(3000, 100, "/usr/bin/node")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if name != "node" {
		t.Fatalf("expected %q, got %q", "node", name)
	}
}

func TestProcessNameAdaptor_Conflict(t *testing.T) {
	a := NewProcessNameAdaptor()

	name1, ok := a.Resolve(3000, 100, "/usr/bin/node")
	if !ok || name1 != "node" {
		t.Fatalf("first process: expected (%q, true), got (%q, %v)", "node", name1, ok)
	}

	name2, ok := a.Resolve(3001, 101, "/usr/bin/node")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if name2 != "node-3001" {
		t.Fatalf("second process: expected %q, got %q", "node-3001", name2)
	}
}

func TestProcessNameAdaptor_Fallback(t *testing.T) {
	a := NewProcessNameAdaptor()

	name, ok := a.Resolve(9999, 0, "")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if name != "port-9999" {
		t.Fatalf("expected %q, got %q", "port-9999", name)
	}
}

func TestChain_FirstSuccessWins(t *testing.T) {
	c := Chain{
		stubAdaptor{ok: false},
		stubAdaptor{name: "from-second", ok: true},
		stubAdaptor{name: "from-third", ok: true},
	}

	name, ok := c.Resolve(1234, 1, "/bin/x")
	if !ok || name != "from-second" {
		t.Fatalf("expected (%q, true), got (%q, %v)", "from-second", name, ok)
	}
}

func TestChain_NoneResolve(t *testing.T) {
	c := Chain{stubAdaptor{ok: false}, stubAdaptor{ok: false}}

	_, ok := c.Resolve(1234, 1, "/bin/x")
	if ok {
		t.Fatal("expected ok=false when no adaptor resolves")
	}
}

type stubAdaptor struct {
	name string
	ok   bool
}

func (s stubAdaptor) Resolve(int32, int32, string) (string, bool) {
	return s.name, s.ok
}
