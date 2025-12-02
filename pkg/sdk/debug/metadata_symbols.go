package debug

import (
	"debug/elf"
	"debug/macho"
	"fmt"
	"runtime"
	"strings"
)

func (p *FunctionMetadataProvider) searchReflectionForFunction(funcName string) (uint64, error) {
	// Note: Runtime reflection in Go (runtime.FuncForPC) works by PC, not name lookup.
	// However, we can't easily iterate ALL functions efficiently without DWARF or symbol table access.
	// But since we are inside the process, we can try to find the symbol if we had a way to lookup by name.
	//
	// Go's runtime doesn't expose "LookupByName".
	//
	// ALTERNATIVE: We can iterate over the symbol table using `nm` or parsing the symbol table directly
	// from the binary (which is what we do below).
	//
	// If DWARF is missing (-w), the symbol table usually remains (unless -s is also used).
	// If -s is used, we are truly blind unless we use `runtime.FuncForPC` on known pointers,
	// but we can't discover arbitrary functions by name easily.
	//
	// However, for this implementation, we will try to parse the Symbol Table from the binary
	// using debug/elf or debug/macho, which is distinct from DWARF.

	// Re-open file to read symbol table
	if p.closer == nil {
		// Try to open file again
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
