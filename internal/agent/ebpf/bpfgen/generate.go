package bpfgen

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go uprobe ../bpf/uprobe.c -- -I../bpf/headers
