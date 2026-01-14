package ask

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/config"
)

func TestConversationPersistence(t *testing.T) {
	// Setup minimalist agent
	askCfg := &config.AskConfig{
		DefaultModel: "mock:script",
	}
	colonyCfg := &config.ColonyConfig{
		ColonyID: "p-test",
	}

	// We can't easily use NewAgent because it tries to connect to MCP.
	// However, SetConversationHistory and GetConversationHistory only depend on the struct fields.
	// So we can instantiate the struct directly for THIS specific unit test content.
	// If the methods grew to depend on other things, we'd need a proper constructor or mocks.
	agent := &Agent{
		config:        askCfg,
		colonyConfig:  colonyCfg,
		conversations: make(map[string]*Conversation),
		debug:         true,
	}

	t.Run("SetConversationHistory", func(t *testing.T) {
		messages := []Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		}
		conversationID := "conv-123"

		agent.SetConversationHistory(conversationID, messages)

		// Verify internal state
		require.Contains(t, agent.conversations, conversationID)
		conv := agent.conversations[conversationID]
		// ID is private, but checking map key presence confirms it's stored correctly.
		msgs := conv.GetMessages()
		assert.Equal(t, 2, len(msgs))
		assert.Equal(t, "user", msgs[0].Role)
		assert.Equal(t, "Hello", msgs[0].Content)
	})

	t.Run("GetConversationHistory", func(t *testing.T) {
		conversationID := "conv-456"
		expectedMessages := []Message{
			{Role: "user", Content: "Question"},
			{Role: "assistant", Content: "Answer"},
		}

		// Pre-populate
		agent.SetConversationHistory(conversationID, expectedMessages)

		// Retrieve
		history := agent.GetConversationHistory(conversationID)

		assert.NotNil(t, history)
		assert.Equal(t, len(expectedMessages), len(history))
		assert.Equal(t, expectedMessages[0].Content, history[0].Content)
	})

	t.Run("GetNonExistentContracts", func(t *testing.T) {
		history := agent.GetConversationHistory("non-existent")
		assert.Nil(t, history)
	})
}
