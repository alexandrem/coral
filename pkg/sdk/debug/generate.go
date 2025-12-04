package debug

//go:generate go build -o testdata/sample_with_dwarf testdata/sample.go
//go:generate go build -ldflags=-w -o testdata/sample_without_dwarf testdata/sample.go
