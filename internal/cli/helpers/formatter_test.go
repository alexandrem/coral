package helpers

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestData is a test struct with header tags.
type TestData struct {
	Name  string `header:"Name"`
	Value int    `header:"Value"`
	Extra string // No header tag, should be ignored
}

func TestNewFormatter(t *testing.T) {
	tests := []struct {
		name    string
		format  OutputFormat
		wantErr bool
	}{
		{
			name:    "table formatter",
			format:  FormatTable,
			wantErr: false,
		},
		{
			name:    "json formatter",
			format:  FormatJSON,
			wantErr: false,
		},
		{
			name:    "csv formatter",
			format:  FormatCSV,
			wantErr: false,
		},
		{
			name:    "unsupported format",
			format:  OutputFormat("unsupported"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewFormatter(tt.format)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewFormatter() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got == nil {
				t.Errorf("NewFormatter() returned nil formatter")
			}
		})
	}
}

func TestJSONFormatter_Format(t *testing.T) {
	tests := []struct {
		name    string
		data    interface{}
		wantErr bool
	}{
		{
			name: "format slice of structs",
			data: []TestData{
				{Name: "test1", Value: 1, Extra: "ignored"},
				{Name: "test2", Value: 2, Extra: "also ignored"},
			},
			wantErr: false,
		},
		{
			name:    "format empty slice",
			data:    []TestData{},
			wantErr: false,
		},
		{
			name: "format single struct",
			data: TestData{
				Name:  "single",
				Value: 42,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &JSONFormatter{}
			buf := &bytes.Buffer{}
			err := f.Format(tt.data, buf)
			if (err != nil) != tt.wantErr {
				t.Errorf("JSONFormatter.Format() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify output is valid JSON.
				var result interface{}
				if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
					t.Errorf("JSONFormatter.Format() produced invalid JSON: %v", err)
				}
			}
		})
	}
}

func TestTableFormatter_Format(t *testing.T) {
	tests := []struct {
		name         string
		data         interface{}
		wantErr      bool
		wantContains []string // Strings that should be in output
	}{
		{
			name: "format slice of structs",
			data: []TestData{
				{Name: "test1", Value: 1, Extra: "ignored"},
				{Name: "test2", Value: 2, Extra: "ignored"},
			},
			wantErr: false,
			wantContains: []string{
				"Name", "Value", // Headers
				"test1", "1", // First row
				"test2", "2", // Second row
			},
		},
		{
			name:    "format empty slice",
			data:    []TestData{},
			wantErr: false,
		},
		{
			name: "format non-slice data",
			data: TestData{
				Name:  "single",
				Value: 42,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &TableFormatter{}
			buf := &bytes.Buffer{}
			err := f.Format(tt.data, buf)
			if (err != nil) != tt.wantErr {
				t.Errorf("TableFormatter.Format() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				output := buf.String()
				for _, want := range tt.wantContains {
					if !strings.Contains(output, want) {
						t.Errorf("TableFormatter.Format() output missing %q\nGot: %s", want, output)
					}
				}
			}
		})
	}
}

func TestCSVFormatter_Format(t *testing.T) {
	tests := []struct {
		name         string
		data         interface{}
		wantErr      bool
		wantContains []string // Strings that should be in output
	}{
		{
			name: "format slice of structs",
			data: []TestData{
				{Name: "test1", Value: 1, Extra: "ignored"},
				{Name: "test2", Value: 2, Extra: "ignored"},
			},
			wantErr: false,
			wantContains: []string{
				"Name,Value", // Headers
				"test1,1",    // First row
				"test2,2",    // Second row
			},
		},
		{
			name:         "format empty slice",
			data:         []TestData{},
			wantErr:      false,
			wantContains: []string{},
		},
		{
			name: "format non-slice data",
			data: TestData{
				Name:  "single",
				Value: 42,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &CSVFormatter{}
			buf := &bytes.Buffer{}
			err := f.Format(tt.data, buf)
			if (err != nil) != tt.wantErr {
				t.Errorf("CSVFormatter.Format() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				output := buf.String()
				for _, want := range tt.wantContains {
					if !strings.Contains(output, want) {
						t.Errorf("CSVFormatter.Format() output missing %q\nGot: %s", want, output)
					}
				}
			}
		})
	}
}
