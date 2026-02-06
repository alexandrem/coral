# Developer ConfigurationGuide

## Configuration Validation

Coral provides comprehensive configuration validation through the `Validator`
interface:

```go
import "github.com/coral-mesh/coral/internal/config"

cfg := config.DefaultAgentConfig()

// Validate configuration
if err := cfg.Validate(); err != nil {
    if multiErr, ok := err.(*config.MultiValidationError); ok {
        for _, validationErr := range multiErr.Errors {
            fmt.Printf("- %s: %s\n", validationErr.Field, validationErr.Message)
        }
    } else {
        fmt.Printf("Configuration validation failed: %v\n", err)
    }
}
```

## Layered Configuration Loading

Use the `LayeredLoader` for advanced configuration scenarios in your code:

```go
import "github.com/coral-mesh/coral/internal/config"

// Create loader with all layers enabled (Defaults -> File -> Env)
loader := config.NewLayeredLoader()

// Load agent configuration with layered precedence
cfg, err := loader.LoadAgentConfig("/path/to/agent.yaml")
if err != nil {
    log.Fatalf("Failed to load config: %v", err)
}

// Validate the merged configuration
if err := loader.ValidateConfig(cfg); err != nil {
    log.Fatalf("Invalid configuration: %v", err)
}
```

You can also selective enable/disable layers:

```go
loader.DisableLayer(config.LayerEnv) // Disable environment variable overrides
```
