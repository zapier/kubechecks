package a2askills_test

import (
	"context"
	"errors"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	a2askillsmocks "github.com/zapier/kubechecks/mocks/a2askills/mocks"
)

// ── Client.Discover ──────────────────────────────────────────────────────────

func TestDiscover(t *testing.T) {
	twoSkills := []a2a.AgentSkill{
		{ID: "knowledge_search", Name: "Knowledge Search", Description: "Search the knowledge base"},
		{ID: "knowledge_write", Name: "Knowledge Write", Description: "Write to the knowledge base"},
	}

	tests := []struct {
		name       string
		setupMock  func(*a2askillsmocks.MockClient)
		wantLen    int
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "returns discovered skills",
			setupMock: func(m *a2askillsmocks.MockClient) {
				m.EXPECT().Discover(mock.Anything).Return(twoSkills, nil)
			},
			wantLen: 2,
		},
		{
			name: "empty skill list is not an error",
			setupMock: func(m *a2askillsmocks.MockClient) {
				m.EXPECT().Discover(mock.Anything).Return(nil, nil)
			},
			wantLen: 0,
		},
		{
			name: "agent unreachable returns error",
			setupMock: func(m *a2askillsmocks.MockClient) {
				m.EXPECT().Discover(mock.Anything).Return(nil, errors.New("connection refused"))
			},
			wantErr:    true,
			wantErrMsg: "connection refused",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := a2askillsmocks.NewMockClient(t)
			tc.setupMock(m)

			skills, err := m.Discover(context.Background())

			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tc.wantErrMsg)
				}
				return
			}

			require.NoError(t, err)
			assert.Len(t, skills, tc.wantLen)
		})
	}
}

// ── Client.Call ──────────────────────────────────────────────────────────────

func TestCall(t *testing.T) {
	sampleResult := `{"entries":[{"Domain":"convention","Key":"ack-not-terraform"}],"count":1}`

	tests := []struct {
		name        string
		setupMock   func(*a2askillsmocks.MockClient)
		skill       string
		params      map[string]any
		wantErr     bool
		wantErrMsg  string
		wantContain string
	}{
		{
			name: "success returns result JSON",
			setupMock: func(m *a2askillsmocks.MockClient) {
				m.EXPECT().Call(mock.Anything, "knowledge_search", map[string]any{"query": "pod identity", "limit": float64(5)}).
					Return(sampleResult, nil)
			},
			skill:       "knowledge_search",
			params:      map[string]any{"query": "pod identity", "limit": float64(5)},
			wantContain: "ack-not-terraform",
		},
		{
			name: "agent error propagates",
			setupMock: func(m *a2askillsmocks.MockClient) {
				m.EXPECT().Call(mock.Anything, mock.Anything, mock.Anything).
					Return("", errors.New("unavailable"))
			},
			skill:      "knowledge_search",
			params:     map[string]any{"query": "anything"},
			wantErr:    true,
			wantErrMsg: "unavailable",
		},
		{
			name: "any skill name is accepted",
			setupMock: func(m *a2askillsmocks.MockClient) {
				m.EXPECT().Call(mock.Anything, "custom_skill", mock.Anything).
					Return(`{"result":"ok"}`, nil)
			},
			skill:       "custom_skill",
			params:      map[string]any{"key": "val"},
			wantContain: `"result"`,
		},
		{
			name: "empty params accepted",
			setupMock: func(m *a2askillsmocks.MockClient) {
				m.EXPECT().Call(mock.Anything, "knowledge_search", map[string]any{}).
					Return(sampleResult, nil)
			},
			skill:       "knowledge_search",
			params:      map[string]any{},
			wantContain: "ack-not-terraform",
		},
		{
			name: "context cancellation propagates",
			setupMock: func(m *a2askillsmocks.MockClient) {
				m.EXPECT().Call(mock.Anything, mock.Anything, mock.Anything).
					Return("", context.Canceled)
			},
			skill:      "knowledge_search",
			params:     map[string]any{},
			wantErr:    true,
			wantErrMsg: "context canceled",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := a2askillsmocks.NewMockClient(t)
			tc.setupMock(m)

			result, err := m.Call(context.Background(), tc.skill, tc.params)

			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tc.wantErrMsg)
				}
				return
			}

			require.NoError(t, err)
			if tc.wantContain != "" {
				assert.Contains(t, result, tc.wantContain)
			}
		})
	}
}
