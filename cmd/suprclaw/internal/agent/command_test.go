package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAgentCommand(t *testing.T) {
	cmd := NewAgentCommand()

	require.NotNil(t, cmd)

	assert.Equal(t, "agent", cmd.Use)
	assert.Equal(t, "Interact with the agent directly", cmd.Short)

	assert.Len(t, cmd.Aliases, 0)
	assert.False(t, cmd.HasSubCommands())

	assert.Nil(t, cmd.Run)
	assert.NotNil(t, cmd.RunE)

	assert.Nil(t, cmd.PersistentPreRun)
	assert.Nil(t, cmd.PersistentPostRun)

	assert.True(t, cmd.HasFlags())

	assert.NotNil(t, cmd.Flags().Lookup("debug"))
	assert.NotNil(t, cmd.Flags().Lookup("message"))
	assert.NotNil(t, cmd.Flags().Lookup("session"))
	assert.NotNil(t, cmd.Flags().Lookup("model"))
	assert.NotNil(t, cmd.Flags().Lookup("agent-id"))
}

func TestNewAgentCommand_ForwardsAgentIDFlag(t *testing.T) {
	originalRunner := runAgentCommand
	t.Cleanup(func() { runAgentCommand = originalRunner })

	capturedAgentID := ""
	runAgentCommand = func(message, sessionKey, model, requestedAgentID string, debug bool) error {
		capturedAgentID = requestedAgentID
		return nil
	}

	cmd := NewAgentCommand()
	cmd.SetArgs([]string{"--message", "hello", "--session", "test-session", "--agent-id", "writer"})

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Equal(t, "writer", capturedAgentID)
}
