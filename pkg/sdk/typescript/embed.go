// Package typescript provides the embedded Coral TypeScript SDK source files
// for use by the coral_run MCP tool executor (RFD 093).
package typescript

import "embed"

// FS contains all TypeScript SDK source files, including the skills
// sub-package. It is used by the coral_run executor to extract a local copy
// of the SDK and generate an import map so scripts can import from
// "@coral/sdk" without network access.
//
//go:embed *.ts skills
var FS embed.FS
