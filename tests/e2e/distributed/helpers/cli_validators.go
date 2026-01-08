package helpers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TableValidator validates table-formatted CLI output.
// It checks structure (headers, row count) without being brittle about exact formatting.
type TableValidator struct {
	Headers []string // Expected column headers (case-insensitive)
	MinRows int      // Minimum number of data rows (excluding header)
	MaxRows int      // Maximum number of data rows (0 = unlimited)
}

// ValidateTable checks that table output has expected structure.
// Returns parsed rows for further validation.
func (tv *TableValidator) ValidateTable(t *testing.T, output string) [][]string {
	t.Helper()

	rows := ParseTableOutput(output)

	if len(rows) == 0 {
		t.Fatal("Table output is empty")
	}

	// Validate headers if specified
	if len(tv.Headers) > 0 && len(rows) > 0 {
		headerRow := strings.ToLower(strings.Join(rows[0], " "))
		for _, expectedHeader := range tv.Headers {
			if !strings.Contains(headerRow, strings.ToLower(expectedHeader)) {
				t.Errorf("Table missing expected header %q. Got headers: %v", expectedHeader, rows[0])
			}
		}
	}

	// Count data rows (excluding header)
	dataRows := len(rows) - 1
	if dataRows < 0 {
		dataRows = 0
	}

	// Validate minimum rows
	if tv.MinRows > 0 && dataRows < tv.MinRows {
		t.Errorf("Table has %d data rows, expected at least %d", dataRows, tv.MinRows)
	}

	// Validate maximum rows
	if tv.MaxRows > 0 && dataRows > tv.MaxRows {
		t.Errorf("Table has %d data rows, expected at most %d", dataRows, tv.MaxRows)
	}

	return rows
}

// JSONValidator validates JSON-formatted CLI output.
type JSONValidator struct {
	RequiredFields []string // Fields that must be present
	Type           string   // "array" or "object"
}

// ValidateJSON checks that JSON output has expected structure.
// Returns the parsed JSON data.
func (jv *JSONValidator) ValidateJSON(t *testing.T, jsonStr string) interface{} {
	t.Helper()

	var data interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		t.Fatalf("Failed to parse JSON: %v\nOutput: %s", err, jsonStr)
	}

	// Validate type if specified
	if jv.Type != "" {
		switch jv.Type {
		case "array":
			if _, ok := data.([]interface{}); !ok {
				t.Errorf("Expected JSON array, got: %T", data)
			}
		case "object":
			if _, ok := data.(map[string]interface{}); !ok {
				t.Errorf("Expected JSON object, got: %T", data)
			}
		}
	}

	// Validate required fields for objects
	if len(jv.RequiredFields) > 0 {
		obj, ok := data.(map[string]interface{})
		if !ok {
			t.Errorf("Cannot validate required fields on non-object JSON")
			return data
		}

		for _, field := range jv.RequiredFields {
			if _, exists := obj[field]; !exists {
				t.Errorf("JSON missing required field: %s", field)
			}
		}
	}

	return data
}

// ContainsRow checks if table output contains a row matching specified column values.
// columnValues is a map of column name to expected value.
func ContainsRow(rows [][]string, columnValues map[string]string) bool {
	if len(rows) < 2 {
		return false // No data rows
	}

	// Build column index map from header row
	headers := rows[0]
	colIndex := make(map[string]int)
	for i, header := range headers {
		colIndex[strings.ToLower(header)] = i
	}

	// Check each data row
	for _, row := range rows[1:] {
		match := true
		for column, expectedValue := range columnValues {
			idx, exists := colIndex[strings.ToLower(column)]
			if !exists || idx >= len(row) {
				match = false
				break
			}

			if !strings.Contains(strings.ToLower(row[idx]), strings.ToLower(expectedValue)) {
				match = false
				break
			}
		}

		if match {
			return true
		}
	}

	return false
}

// ValidateErrorMessage checks that error output contains expected message.
func ValidateErrorMessage(t *testing.T, result *CLIResult, expectedMsg string) {
	t.Helper()

	if !result.HasError() {
		t.Errorf("Expected command to fail, but it succeeded")
		return
	}

	if !strings.Contains(strings.ToLower(result.Output), strings.ToLower(expectedMsg)) {
		t.Errorf("Error output missing expected message %q\nGot: %s", expectedMsg, result.Output)
	}
}

// ValidateJSONArray validates that JSON is an array with minimum length.
func ValidateJSONArray(t *testing.T, jsonStr string, minLength int) []interface{} {
	t.Helper()

	validator := &JSONValidator{Type: "array"}
	data := validator.ValidateJSON(t, jsonStr)

	arr, ok := data.([]interface{})
	if !ok {
		t.Fatalf("Expected array, got %T", data)
	}

	if len(arr) < minLength {
		t.Errorf("Array has %d elements, expected at least %d", len(arr), minLength)
	}

	return arr
}

// ValidateJSONObject validates that JSON is an object with required fields.
func ValidateJSONObject(t *testing.T, jsonStr string, requiredFields ...string) map[string]interface{} {
	t.Helper()

	validator := &JSONValidator{
		Type:           "object",
		RequiredFields: requiredFields,
	}
	data := validator.ValidateJSON(t, jsonStr)

	obj, ok := data.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected object, got %T", data)
	}

	return obj
}

// AssertContains checks that output contains expected substring (case-insensitive).
func AssertContains(t *testing.T, output, expected string) {
	t.Helper()

	if !strings.Contains(strings.ToLower(output), strings.ToLower(expected)) {
		t.Errorf("Output missing expected content %q\nOutput: %s", expected, output)
	}
}

// AssertNotContains checks that output does not contain substring (case-insensitive).
func AssertNotContains(t *testing.T, output, unexpected string) {
	t.Helper()

	if strings.Contains(strings.ToLower(output), strings.ToLower(unexpected)) {
		t.Errorf("Output contains unexpected content %q\nOutput: %s", unexpected, output)
	}
}

// PrintTable pretty-prints table rows for debugging.
func PrintTable(t *testing.T, rows [][]string) {
	t.Helper()

	for i, row := range rows {
		t.Logf("Row %d: %v", i, row)
	}
}

// GetColumnValue extracts value from a specific column in a table row.
// Returns empty string if column not found.
func GetColumnValue(headers, row []string, columnName string) string {
	for i, header := range headers {
		if strings.EqualFold(header, columnName) && i < len(row) {
			return row[i]
		}
	}
	return ""
}

// AssertTableRowCount checks that table has expected number of data rows.
func AssertTableRowCount(t *testing.T, rows [][]string, expectedCount int) {
	t.Helper()

	dataRows := len(rows) - 1
	if dataRows < 0 {
		dataRows = 0
	}

	if dataRows != expectedCount {
		t.Errorf("Expected %d data rows, got %d", expectedCount, dataRows)
		PrintTable(t, rows)
	}
}

// ParseJSONResponse parses JSON response and handles errors.
func ParseJSONResponse(t *testing.T, jsonStr string) interface{} {
	t.Helper()

	var data interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		t.Fatalf("Failed to parse JSON: %v\nJSON: %s", err, jsonStr)
	}

	return data
}

// GetJSONField extracts a field from JSON object, returns nil if not found.
func GetJSONField(obj map[string]interface{}, field string) interface{} {
	return obj[field]
}

// GetJSONString extracts a string field from JSON object.
func GetJSONString(t *testing.T, obj map[string]interface{}, field string) string {
	t.Helper()

	value, exists := obj[field]
	if !exists {
		t.Errorf("JSON missing field: %s", field)
		return ""
	}

	str, ok := value.(string)
	if !ok {
		t.Errorf("Field %s is not a string: %T", field, value)
		return ""
	}

	return str
}

// GetJSONFloat extracts a float64 field from JSON object.
func GetJSONFloat(t *testing.T, obj map[string]interface{}, field string) float64 {
	t.Helper()

	value, exists := obj[field]
	if !exists {
		t.Errorf("JSON missing field: %s", field)
		return 0
	}

	// JSON numbers are unmarshaled as float64
	num, ok := value.(float64)
	if !ok {
		t.Errorf("Field %s is not a number: %T", field, value)
		return 0
	}

	return num
}

// ValidateHTTPStatus validates that CLI output indicates a specific HTTP status.
// Useful for testing error responses.
func ValidateHTTPStatus(t *testing.T, result *CLIResult, expectedStatus int) {
	t.Helper()

	statusStr := fmt.Sprintf("%d", expectedStatus)
	if !strings.Contains(result.Output, statusStr) {
		t.Errorf("Expected HTTP status %d in output\nGot: %s", expectedStatus, result.Output)
	}
}
