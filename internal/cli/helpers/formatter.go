package helpers

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"text/tabwriter"
)

// OutputFormat represents the desired output format.
type OutputFormat string

const (
	FormatTable OutputFormat = "table"
	FormatJSON  OutputFormat = "json"
	FormatCSV   OutputFormat = "csv"
)

// Formatter defines the interface for formatting query results.
type Formatter interface {
	Format(data interface{}, writer io.Writer) error
}

// NewFormatter creates a new Formatter for the given format.
func NewFormatter(format OutputFormat) (Formatter, error) {
	switch format {
	case FormatTable:
		return &TableFormatter{}, nil
	case FormatJSON:
		return &JSONFormatter{}, nil
	case FormatCSV:
		return &CSVFormatter{}, nil
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}

// JSONFormatter formats data as JSON.
type JSONFormatter struct{}

func (f *JSONFormatter) Format(data interface{}, writer io.Writer) error {
	enc := json.NewEncoder(writer)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

// TableFormatter formats data as a table using struct tags.
type TableFormatter struct{}

func (f *TableFormatter) Format(data interface{}, writer io.Writer) error {
	val := reflect.ValueOf(data)
	if val.Kind() == reflect.Slice {
		if val.Len() == 0 {
			return nil
		}
		// Use the first element to determine headers
		elemType := val.Index(0).Type()
		headers := getHeaders(elemType)

		w := tabwriter.NewWriter(writer, 0, 0, 3, ' ', 0)
		if _, err := fmt.Fprintln(w, strings.Join(headers, "\t")); err != nil {
			return err
		}

		for i := 0; i < val.Len(); i++ {
			row := getRowValues(val.Index(i))
			if _, err := fmt.Fprintln(w, strings.Join(row, "\t")); err != nil {
				return err
			}
		}
		return w.Flush()
	}
	return fmt.Errorf("data must be a slice")
}

// CSVFormatter formats data as CSV using struct tags.
type CSVFormatter struct{}

func (f *CSVFormatter) Format(data interface{}, writer io.Writer) error {
	val := reflect.ValueOf(data)
	if val.Kind() == reflect.Slice {
		if val.Len() == 0 {
			return nil
		}
		elemType := val.Index(0).Type()
		headers := getHeaders(elemType)

		w := csv.NewWriter(writer)
		if err := w.Write(headers); err != nil {
			return err
		}

		for i := 0; i < val.Len(); i++ {
			row := getRowValues(val.Index(i))
			if err := w.Write(row); err != nil {
				return err
			}
		}
		w.Flush()
		return w.Error()
	}
	return fmt.Errorf("data must be a slice")
}

func getHeaders(t reflect.Type) []string {
	var headers []string
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("header")
		if tag != "" {
			headers = append(headers, tag)
		}
	}
	return headers
}

func getRowValues(v reflect.Value) []string {
	var values []string
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		if field.Tag.Get("header") != "" {
			val := v.Field(i)
			values = append(values, fmt.Sprintf("%v", val.Interface()))
		}
	}
	return values
}
