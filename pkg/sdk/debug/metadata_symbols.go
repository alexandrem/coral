package debug

import (
	"debug/elf"
	"debug/macho"
	"fmt"
	"runtime"
	"strings"
)

func (p *FunctionMetadataProvider) searchReflectionForFunction(funcName string) (uint64, error) {
	// Parse the symbol table to find the function by name.
	// This works even when DWARF symbols are stripped (-ldflags=-w).

	switch runtime.GOOS {
	case "linux":
		f, err := elf.Open(p.binaryPath)
		if err != nil {
			return 0, err
		}
		defer f.Close() // nolint:errcheck
		symbols, err := f.Symbols()
		if err != nil {
			return 0, fmt.Errorf("failed to read ELF symbols: %w", err)
		}
		for _, sym := range symbols {
			if sym.Name == funcName || strings.HasSuffix(sym.Name, "."+funcName) {
				if elf.ST_TYPE(sym.Info) == elf.STT_FUNC {
					return sym.Value, nil
				}
			}
		}
	case "darwin":
		f, err := macho.Open(p.binaryPath)
		if err != nil {
			return 0, err
		}
		defer f.Close() // nolint:errcheck
		if f.Symtab == nil {
			return 0, fmt.Errorf("no symbol table found")
		}
		for _, sym := range f.Symtab.Syms {
			if sym.Name == funcName || strings.HasSuffix(sym.Name, "."+funcName) {
				// Mach-O symbols often have a leading underscore
				cleanName := strings.TrimPrefix(sym.Name, "_")
				if cleanName == funcName || strings.HasSuffix(cleanName, "."+funcName) {
					return sym.Value, nil
				}
			}
		}
	}

	return 0, fmt.Errorf("function %s not found in symbol table", funcName)
}

// listFunctionsFromSymbols lists functions from the binary symbol table.
func (p *FunctionMetadataProvider) listFunctionsFromSymbols(pattern string) ([]string, error) {
	var functions []string

	switch runtime.GOOS {
	case "linux":
		f, err := elf.Open(p.binaryPath)
		if err != nil {
			return nil, err
		}
		defer f.Close() // nolint:errcheck
		symbols, err := f.Symbols()
		if err != nil {
			return nil, err
		}
		for _, sym := range symbols {
			if elf.ST_TYPE(sym.Info) == elf.STT_FUNC && sym.Name != "" {
				if matchesPattern(sym.Name, pattern) {
					functions = append(functions, sym.Name)
				}
			}
		}
	case "darwin":
		f, err := macho.Open(p.binaryPath)
		if err != nil {
			return nil, err
		}
		defer f.Close() // nolint:errcheck
		if f.Symtab != nil {
			for _, sym := range f.Symtab.Syms {
				name := strings.TrimPrefix(sym.Name, "_")
				if name != "" && matchesPattern(name, pattern) {
					functions = append(functions, name)
				}
			}
		}
	}

	return functions, nil
}
