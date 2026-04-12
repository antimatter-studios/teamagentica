package agentkit

import (
	"fmt"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// channelSink implements EventSink by writing AgentStreamEvents to a channel.
// It also accumulates the full response text for the done event.
type channelSink struct {
	ch           chan<- pluginsdk.AgentStreamEvent
	fullResponse string
}

// newChannelSink creates an EventSink that writes to the given channel.
func newChannelSink(ch chan<- pluginsdk.AgentStreamEvent) *channelSink {
	return &channelSink{ch: ch}
}

func (s *channelSink) SendText(text string) error {
	s.fullResponse += text
	s.ch <- pluginsdk.StreamToken(text)
	return nil
}

// FullResponse returns all text accumulated via SendText.
func (s *channelSink) FullResponse() string {
	return s.fullResponse
}

func (s *channelSink) SendToolCall(call ToolCall) error {
	s.ch <- pluginsdk.StreamToolCall(call.Name, string(call.Arguments))
	return nil
}

func (s *channelSink) SendDone() error {
	// Done is sent by the runtime after the tool loop completes, not by the sink.
	return nil
}

func (s *channelSink) SendError(err error) error {
	s.ch <- pluginsdk.StreamError(fmt.Sprintf("%v", err))
	return nil
}
