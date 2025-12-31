package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// LoadFromEnv loads configuration values from environment variables.
// It uses the `env` struct tag to determine which environment variable to read.
// This function recursively processes nested structs.
func LoadFromEnv(cfg interface{}) error {
	return loadFromEnv(reflect.ValueOf(cfg), "")
}

// loadFromEnv recursively loads environment variables into a config struct.
func loadFromEnv(v reflect.Value, prefix string) error {
	// Dereference pointer
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}

	// Only process structs
	if v.Kind() != reflect.Struct {
		return nil
	}

	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		// Skip unexported fields
		if !field.CanSet() {
			continue
		}

		// Get env tag
		envTag := fieldType.Tag.Get("env")

		// Handle nested structs
		if field.Kind() == reflect.Struct {
			if err := loadFromEnv(field, prefix); err != nil {
				return err
			}
			continue
		}

		// Skip if no env tag
		if envTag == "" {
			continue
		}

		// Get environment variable value
		envValue := os.Getenv(envTag)
		if envValue == "" {
			continue
		}

		// Set the field value based on its type
		if err := setFieldValue(field, envValue, fieldType.Name, envTag); err != nil {
			return err
		}
	}

	return nil
}

// setFieldValue sets a field value from a string environment variable.
func setFieldValue(field reflect.Value, value string, fieldName string, envVar string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(value)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// Check if it's a time.Duration
		if field.Type() == reflect.TypeOf(time.Duration(0)) {
			duration, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("invalid duration for %s (%s): %w", fieldName, envVar, err)
			}
			field.SetInt(int64(duration))
		} else {
			intVal, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid integer for %s (%s): %w", fieldName, envVar, err)
			}
			field.SetInt(intVal)
		}

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		uintVal, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid unsigned integer for %s (%s): %w", fieldName, envVar, err)
		}
		field.SetUint(uintVal)

	case reflect.Bool:
		boolVal, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean for %s (%s): %w", fieldName, envVar, err)
		}
		field.SetBool(boolVal)

	case reflect.Float32, reflect.Float64:
		floatVal, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("invalid float for %s (%s): %w", fieldName, envVar, err)
		}
		field.SetFloat(floatVal)

	case reflect.Slice:
		// Handle string slices (comma-separated values)
		if field.Type().Elem().Kind() == reflect.String {
			values := strings.Split(value, ",")
			// Trim whitespace from each value
			for i, v := range values {
				values[i] = strings.TrimSpace(v)
			}
			field.Set(reflect.ValueOf(values))
		} else {
			return fmt.Errorf("unsupported slice type for %s (%s)", fieldName, envVar)
		}

	default:
		return fmt.Errorf("unsupported type %s for %s (%s)", field.Kind(), fieldName, envVar)
	}

	return nil
}

// MergeFromEnv merges environment variables into an existing config.
// This is a convenience wrapper around LoadFromEnv.
func MergeFromEnv(cfg interface{}) error {
	return LoadFromEnv(cfg)
}
