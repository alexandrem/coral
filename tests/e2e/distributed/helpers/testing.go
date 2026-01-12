package helpers

import (
	"github.com/stretchr/testify/require"
)

// T defines the subset of testing functionality required.
type T interface {
	require.TestingT
	Helper()
}
