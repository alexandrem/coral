package debug

import (
	"fmt"
	"strings"
)

func (p *FunctionMetadataProvider) searchReflectionForFunction(funcName string) (uint64, error) {
	// Load cached symbol table (loaded once at first use).
	// This works even when DWARF symbols are stripped (-ldflags=-w).
	symbols, err := p.loadSymbols()
	if err != nil {
		return 0, err
	}

	// Search cached symbols for the function.
	for _, sym := range symbols {
		if sym.Name == funcName || strings.HasSuffix(sym.Name, "."+funcName) {
			return sym.Value, nil
		}
	}

	return 0, fmt.Errorf("function %s not found in symbol table", funcName)
}

// listFunctionsFromSymbols lists functions from the binary symbol table.
func (p *FunctionMetadataProvider) listFunctionsFromSymbols(pattern string) ([]string, error) {
	// Load cached symbol table (loaded once at first use).
	symbols, err := p.loadSymbols()
	if err != nil {
		return nil, err
	}

	// Filter cached symbols by pattern.
	var functions []string
	for _, sym := range symbols {
		if matchesPattern(sym.Name, pattern) {
			functions = append(functions, sym.Name)
		}
	}

	return functions, nil
}
