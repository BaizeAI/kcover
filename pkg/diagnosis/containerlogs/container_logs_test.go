package containerlogs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLinesBeforeAndAfter(t *testing.T) {
	lines := getLinesBeforeAndAfter([]string{"1", "2", "3", "4", "5", "6"}, 0, 2, 2)
	require.Len(t, lines, 3)
	assert.Equal(t, []string{"1", "2", "3"}, lines)

	lines = getLinesBeforeAndAfter([]string{"1", "2", "3", "4", "5", "6"}, 0, 3, 2)
	require.Len(t, lines, 3)
	assert.Equal(t, []string{"1", "2", "3"}, lines)

	lines = getLinesBeforeAndAfter([]string{"1", "2", "3", "4", "5", "6"}, 4, 100, 100)
	require.Len(t, lines, 6)
	assert.Equal(t, []string{"1", "2", "3", "4", "5", "6"}, lines)

	lines = getLinesBeforeAndAfter([]string{"1", "2", "3", "4", "5", "6"}, 7, 100, 100)
	require.Len(t, lines, 6)
	assert.Equal(t, []string{"1", "2", "3", "4", "5", "6"}, lines)
}
