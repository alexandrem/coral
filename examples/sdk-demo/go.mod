module github.com/coral-mesh/coral/examples/sdk-demo

go 1.25.1

replace github.com/coral-mesh/coral => ../../

require github.com/coral-mesh/coral v0.0.0-00010101000000-000000000000

require (
	connectrpc.com/connect v1.19.1 // indirect
	golang.org/x/net v0.46.0 // indirect
	golang.org/x/text v0.30.0 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
)
