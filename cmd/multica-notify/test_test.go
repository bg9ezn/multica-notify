package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bg9ezn/multica-notify/internal/config"
)

func configured() []config.ChannelConfig {
	return []config.ChannelConfig{
		{Name: "wecom-bot", Type: "apprise"},
		{Name: "phone", Type: "ntfy"},
		{Name: "debug", Type: "webhook"},
	}
}

func TestSelectChannelsEmptyWantsAll(t *testing.T) {
	got, err := selectChannels(configured(), nil)
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestSelectChannelsByNameKeepsConfigOrder(t *testing.T) {
	got, err := selectChannels(configured(), []string{"phone", "wecom-bot"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "wecom-bot", got[0].Name, "order must follow the configuration")
	assert.Equal(t, "phone", got[1].Name)
}

func TestSelectChannelsUnknownNameListsConfigured(t *testing.T) {
	_, err := selectChannels(configured(), []string{"wecom-bot", "typo"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"typo"`)
	assert.Contains(t, err.Error(), "wecom-bot, phone, debug")
}

func TestValidateNotifyType(t *testing.T) {
	for _, ok := range []string{"info", "success", "warning", "error"} {
		assert.NoError(t, validateNotifyType(ok), ok)
	}
	assert.Error(t, validateNotifyType("critical"))
	assert.Error(t, validateNotifyType(""))
}

func TestValidateNotifyType_EmptyIsRejected(t *testing.T) {
	// The flag defaults to "info", so an empty value can only be deliberate.
	assert.Error(t, validateNotifyType(""))
}
