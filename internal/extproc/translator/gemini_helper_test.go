// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"github.com/openai/openai-go"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
	"k8s.io/utils/ptr"

	openaischema "github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

func TestOpenAIMessagesToGeminiContents(t *testing.T) {
	tests := []struct {
		name                      string
		messages                  []openai.ChatCompletionMessageParamUnion
		expectedErrorMsg          string
		expectedContents          []genai.Content
		expectedSystemInstruction *genai.Content
	}{
		{
			name: "happy-path",
			messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
						Role: openaischema.ChatMessageRoleDeveloper,
						Content: openai.ChatCompletionDeveloperMessageParamContentUnion{
							OfString: openai.Opt("This is a developer message"),
						},
					},
				},
				{
					OfSystem: &openai.ChatCompletionSystemMessageParam{
						Role: openaischema.ChatMessageRoleSystem,
						Content: openai.ChatCompletionSystemMessageParamContentUnion{
							OfString: openai.Opt("This is a system message"),
						},
					},
				},
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role: openaischema.ChatMessageRoleUser,
						Content: openai.ChatCompletionUserMessageParamContentUnion{
							OfString: openai.Opt("This is a user message"),
						},
					},
				},
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Role:  openaischema.ChatMessageRoleAssistant,
						Audio: openai.ChatCompletionAssistantMessageParamAudio{},
						Content: openai.ChatCompletionAssistantMessageParamContentUnion{
							OfString: openai.Opt("This is a assistant message"),
						},
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								ID: "tool_call_1",
								Function: openai.ChatCompletionMessageToolCallFunctionParam{
									Name:      "example_tool",
									Arguments: "{\"param1\":\"value1\"}",
								},
								Type: openaischema.ChatCompletionMessageToolCallTypeFunction,
							},
						},
					},
				},
				{
					OfTool: &openai.ChatCompletionToolMessageParam{
						ToolCallID: "tool_call_1",
						Content: openai.ChatCompletionToolMessageParamContentUnion{
							OfString: openai.Opt("This is a message from the example_tool"),
						},
					},
				},
			},
			expectedContents: []genai.Content{
				{
					Parts: []*genai.Part{
						{Text: "This is a user message"},
					},
					Role: genai.RoleUser,
				},
				{
					Role: genai.RoleModel,
					Parts: []*genai.Part{
						{
							FunctionCall: &genai.FunctionCall{
								Name: "example_tool",
								Args: map[string]any{
									"param1": "value1",
								},
							},
						},
						{Text: "This is a assistant message"},
					},
				},
				{
					Role: genai.RoleUser,
					Parts: []*genai.Part{
						{
							FunctionResponse: &genai.FunctionResponse{
								Name: "example_tool",
								Response: map[string]any{
									"output": "This is a message from the example_tool",
								},
							},
						},
					},
				},
			},
			expectedSystemInstruction: &genai.Content{
				Parts: []*genai.Part{
					{Text: "This is a developer message"},
					{Text: "This is a system message"},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			contents, systemInstruction, err := openAIMessagesToGeminiContents(tc.messages)

			if tc.expectedErrorMsg != "" || err != nil {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErrorMsg)
			} else {
				if d := cmp.Diff(tc.expectedContents, contents); d != "" {
					t.Errorf("Gemini Contents mismatch (-want +got):\n%s", d)
				}
				if d := cmp.Diff(tc.expectedSystemInstruction, systemInstruction); d != "" {
					t.Errorf("SystemInstruction mismatch (-want +got):\n%s", d)
				}
			}
		})
	}
}

// TestAssistantMsgToGeminiParts tests the assistantMsgToGeminiParts function.
func TestAssistantMsgToGeminiParts(t *testing.T) {
	tests := []struct {
		name              string
		msg               openai.ChatCompletionAssistantMessageParam
		expectedParts     []*genai.Part
		expectedToolCalls map[string]string
		expectedErrorMsg  string
	}{
		{
			name: "empty text content",
			msg: openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.Opt(""),
				},
				Role: openaischema.ChatMessageRoleAssistant,
			},
			expectedParts:     nil,
			expectedToolCalls: map[string]string{},
		},
		{
			name: "simple text content",
			msg: openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.Opt("Hello, I'm an AI assistant"),
				},
				Role: openaischema.ChatMessageRoleAssistant,
			},
			expectedParts: []*genai.Part{
				genai.NewPartFromText("Hello, I'm an AI assistant"),
			},
			expectedToolCalls: map[string]string{},
		},
		// Currently noting is returned for refusal messages.
		{
			name: "text content with refusal message",
			msg: openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfArrayOfContentParts: []openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
						{
							OfRefusal: &openai.ChatCompletionContentPartRefusalParam{
								Refusal: "Response was refused",
							},
						},
					},
				},
				Role: openaischema.ChatMessageRoleAssistant,
			},
			expectedParts:     nil,
			expectedToolCalls: map[string]string{},
		},
		{
			name: "content with an array of texts",
			msg: openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfArrayOfContentParts: []openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
						{
							OfText: &openai.ChatCompletionContentPartTextParam{
								Text: "Hello, I'm an AI assistant",
							},
						},
						{
							OfText: &openai.ChatCompletionContentPartTextParam{
								Text: "How can I assist you today?",
							},
						},
					},
				},
				Role: openaischema.ChatMessageRoleAssistant,
			},
			expectedParts: []*genai.Part{
				genai.NewPartFromText("Hello, I'm an AI assistant"),
				genai.NewPartFromText("How can I assist you today?"),
			},
			expectedToolCalls: map[string]string{},
		},
		{
			name: "tool calls without content",
			msg: openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.Opt(""),
				},
				Role: openaischema.ChatMessageRoleAssistant,
				ToolCalls: []openai.ChatCompletionMessageToolCallParam{
					{
						ID: "call_123",
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      "get_weather",
							Arguments: `{"location":"New York","unit":"celsius"}`,
						},
						Type: openaischema.ChatCompletionMessageToolCallTypeFunction,
					},
				},
			},
			expectedParts: []*genai.Part{
				{
					FunctionCall: &genai.FunctionCall{
						Args: map[string]any{"location": "New York", "unit": "celsius"},
						Name: "get_weather",
					},
				},
			},
			expectedToolCalls: map[string]string{
				"call_123": "get_weather",
			},
		},
		{
			name: "multiple tool calls with content",
			msg: openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.Opt("I'll help you with that"),
				},
				Role: openaischema.ChatMessageRoleAssistant,
				ToolCalls: []openai.ChatCompletionMessageToolCallParam{
					{
						ID: "call_789",
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      "get_weather",
							Arguments: `{"location":"New York","unit":"celsius"}`,
						},
						Type: openaischema.ChatCompletionMessageToolCallTypeFunction,
					},
					{
						ID: "call_abc",
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      "get_time",
							Arguments: `{"timezone":"EST"}`,
						},
						Type: openaischema.ChatCompletionMessageToolCallTypeFunction,
					},
				},
			},
			expectedParts: []*genai.Part{
				genai.NewPartFromFunctionCall("get_weather", map[string]any{
					"location": "New York",
					"unit":     "celsius",
				}),
				genai.NewPartFromFunctionCall("get_time", map[string]any{
					"timezone": "EST",
				}),
				genai.NewPartFromText("I'll help you with that"),
			},
			expectedToolCalls: map[string]string{
				"call_789": "get_weather",
				"call_abc": "get_time",
			},
		},
		{
			name: "invalid tool call arguments",
			msg: openai.ChatCompletionAssistantMessageParam{
				Role: openaischema.ChatMessageRoleAssistant,
				ToolCalls: []openai.ChatCompletionMessageToolCallParam{
					{
						ID: "call_def",
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      "get_weather",
							Arguments: `{"location":"New York"`, // Invalid JSON.
						},
						Type: openaischema.ChatCompletionMessageToolCallTypeFunction,
					},
				},
			},
			expectedErrorMsg: "function arguments should be valid json string",
		},
		{
			name: "nil content",
			msg: openai.ChatCompletionAssistantMessageParam{
				Role: openaischema.ChatMessageRoleAssistant,
			},
			expectedParts:     nil,
			expectedToolCalls: map[string]string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parts, toolCalls, err := assistantMsgToGeminiParts(&tc.msg)

			if tc.expectedErrorMsg != "" || err != nil {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErrorMsg)
			} else {
				require.NoError(t, err)
				if d := cmp.Diff(tc.expectedParts, parts); d != "" {
					t.Errorf("Parts mismatch (-want +got):\n%s", d)
				}
				if d := cmp.Diff(tc.expectedToolCalls, toolCalls); d != "" {
					t.Errorf("Tools mismatch (-want +got):\n%s", d)
				}
			}
		})
	}
}

func TestDeveloperMsgToGeminiParts(t *testing.T) {
	tests := []struct {
		name             string
		msg              openai.ChatCompletionDeveloperMessageParam
		expectedParts    []*genai.Part
		expectedErrorMsg string
	}{
		{
			name: "string content",
			msg: openai.ChatCompletionDeveloperMessageParam{
				Content: openai.ChatCompletionDeveloperMessageParamContentUnion{
					OfString: openai.Opt("This is a system message"),
				},
				Role: openaischema.ChatMessageRoleSystem,
			},
			expectedParts: []*genai.Part{
				{Text: "This is a system message"},
			},
		},
		{
			name: "content as string array",
			msg: openai.ChatCompletionDeveloperMessageParam{
				Content: openai.ChatCompletionDeveloperMessageParamContentUnion{
					OfArrayOfContentParts: []openai.ChatCompletionContentPartTextParam{
						{Text: "This is a system message"},
						{Text: "It can be multiline"},
					},
				},
				Role: openaischema.ChatMessageRoleSystem,
			},
			expectedParts: []*genai.Part{
				{Text: "This is a system message"},
				{Text: "It can be multiline"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			content, err := developerMsgToGeminiParts(&tc.msg)

			if tc.expectedErrorMsg != "" || err != nil {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErrorMsg)
			} else {
				require.NoError(t, err)
				if d := cmp.Diff(tc.expectedParts, content); d != "" {
					t.Errorf("Content mismatch (-want +got):\n%s", d)
				}
			}
		})
	}
}

func TestToolMsgToGeminiParts(t *testing.T) {
	tests := []struct {
		name             string
		msg              openai.ChatCompletionToolMessageParam
		knownToolCalls   map[string]string
		expectedPart     *genai.Part
		expectedErrorMsg string
	}{
		{
			name: "Tool message with string content",
			msg: openai.ChatCompletionToolMessageParam{
				Content: openai.ChatCompletionToolMessageParamContentUnion{
					OfString: openai.Opt("This is a tool message"),
				},
				Role:       openaischema.ChatMessageRoleTool,
				ToolCallID: "tool_123",
			},
			knownToolCalls: map[string]string{"tool_123": "get_weather"},
			expectedPart: &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name:     "get_weather",
					Response: map[string]interface{}{"output": "This is a tool message"},
				},
			},
		},
		{
			name: "Tool message with string array content",
			msg: openai.ChatCompletionToolMessageParam{
				Content: openai.ChatCompletionToolMessageParamContentUnion{
					OfArrayOfContentParts: []openai.ChatCompletionContentPartTextParam{
						{
							Text: "This is a tool message. ",
						},
						{
							Type: openaischema.ChatCompletionContentPartTextTypeText,
							Text: "And this is another part",
						},
					},
				},
				Role:       openaischema.ChatMessageRoleTool,
				ToolCallID: "tool_123",
			},
			knownToolCalls: map[string]string{"tool_123": "get_weather"},
			expectedPart: &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name:     "get_weather",
					Response: map[string]interface{}{"output": "This is a tool message. And this is another part"},
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parts, err := toolMsgToGeminiParts(&tc.msg, tc.knownToolCalls)

			if tc.expectedErrorMsg != "" || err != nil {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErrorMsg)
			} else {
				require.NoError(t, err)
				if d := cmp.Diff(tc.expectedPart, parts); d != "" {
					t.Errorf("Parts mismatch (-want +got):\n%s", d)
				}
			}
		})
	}
}

// TestUserMsgToGeminiParts tests the gcpPartsFromUserMsgToGeminiParts function with different inputs.
func TestUserMsgToGeminiParts(t *testing.T) {
	tests := []struct {
		name           string
		msg            openai.ChatCompletionUserMessageParam
		expectedParts  []*genai.Part
		expectedErrMsg string
	}{
		{
			name: "simple string content",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openaischema.ChatMessageRoleUser,
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: openai.Opt("Hello, how are you?"),
				},
			},
			expectedParts: []*genai.Part{
				{Text: "Hello, how are you?"},
			},
		},
		{
			name: "empty string content",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openaischema.ChatMessageRoleUser,
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: openai.Opt(""),
				},
			},
			expectedParts: nil,
		},
		{
			name: "array with multiple text contents",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openaischema.ChatMessageRoleUser,
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
						{
							OfText: &openai.ChatCompletionContentPartTextParam{
								Type: openaischema.ChatCompletionContentPartTextTypeText,
								Text: "First message",
							},
						},
						{
							OfText: &openai.ChatCompletionContentPartTextParam{
								Type: openaischema.ChatCompletionContentPartTextTypeText,
								Text: "Second message",
							},
						},
					},
				},
			},
			expectedParts: []*genai.Part{
				{Text: "First message"},
				{Text: "Second message"},
			},
		},
		{
			name: "image content with URL",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openaischema.ChatMessageRoleUser,
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
						{
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
								Type: openaischema.ChatCompletionContentPartImageTypeImageURL,
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "https://example.com/image.jpg",
								},
							},
						},
					},
				},
			},
			expectedParts: []*genai.Part{
				{FileData: &genai.FileData{FileURI: "https://example.com/image.jpg", MIMEType: "image/jpeg"}},
			},
		},
		{
			name: "empty image URL",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openaischema.ChatMessageRoleUser,
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
						{
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
								Type: openaischema.ChatCompletionContentPartImageTypeImageURL,
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "",
								},
							},
						},
					},
				},
			},
			expectedParts: nil,
		},
		{
			name: "invalid image URL",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openaischema.ChatMessageRoleUser,
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
						{
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
								Type: openaischema.ChatCompletionContentPartImageTypeImageURL,
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: ":%invalid-url%:",
								},
							},
						},
					},
				},
			},
			expectedErrMsg: "invalid image URL",
		},
		{
			name: "mixed content - text and image",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openaischema.ChatMessageRoleUser,
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
						{
							OfText: &openai.ChatCompletionContentPartTextParam{
								Type: openaischema.ChatCompletionContentPartTextTypeText,
								Text: "Check this image:",
							},
						},
						{
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
								Type: openaischema.ChatCompletionContentPartImageTypeImageURL,
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "https://example.com/image.jpg",
								},
							},
						},
					},
				},
			},
			expectedParts: []*genai.Part{
				{Text: "Check this image:"},
				{FileData: &genai.FileData{FileURI: "https://example.com/image.jpg", MIMEType: "image/jpeg"}},
			},
		},
		{
			name: "data URI image content",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openaischema.ChatMessageRoleUser,
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
						{
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
								Type: openaischema.ChatCompletionContentPartImageTypeImageURL,
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQEAYABgAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/2wBDAQkJCQwLDBgNDRgyIRwhMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjL/wAARCAABAAEDASIAAhEBAxEB/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL/8QAtRAAAgEDAwIEAwUFBAQAAAF9AQIDAAQRBRIhMUEGE1FhByJxFDKBkaEII0KxwRVS0fAkM2JyggkKFhcYGRolJicoKSo0NTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqDhIWGh4iJipKTlJWWl5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uHi4+Tl5ufo6erx8vP09fb3+Pn6/8QAHwEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoL/8QAtREAAgECBAQDBAcFBAQAAQJ3AAECAxEEBSExBhJBUQdhcRMiMoEIFEKRobHBCSMzUvAVYnLRChYkNOEl8RcYGRomJygpKjU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6goOEhYaHiImKkpOUlZaXmJmaoqOkpaanqKmqsrO0tba3uLm6wsPExcbHyMnK0tPU1dbX2Nna4uPk5ebn6Onq8vP09fb3+Pn6/9oADAMBAAIRAxEAPwD3+iiigD//2Q==",
								},
							},
						},
					},
				},
			},
			expectedParts: []*genai.Part{
				{
					InlineData: &genai.Blob{
						Data:     []byte("This field is ignored during testcase comparison"),
						MIMEType: "image/jpeg",
					},
				},
			},
		},
		{
			name: "invalid data URI format",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openaischema.ChatMessageRoleUser,
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
						{
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
								Type: openaischema.ChatCompletionContentPartImageTypeImageURL,
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "data:invalid-format",
								},
							},
						},
					},
				},
			},
			expectedErrMsg: "data uri does not have a valid format",
		},
		{
			name: "audio content - not supported",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openaischema.ChatMessageRoleUser,
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
						{
							OfInputAudio: &openai.ChatCompletionContentPartInputAudioParam{
								Type: "audio",
							},
						},
					},
				},
			},
			expectedErrMsg: "audio content not supported yet",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parts, err := userMsgToGeminiParts(&tc.msg)

			if tc.expectedErrMsg != "" || err != nil {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErrMsg)
			} else {
				if d := cmp.Diff(tc.expectedParts, parts, cmpopts.IgnoreFields(genai.Blob{}, "Data")); d != "" {
					t.Errorf("Parts mismatch (-want +got):\n%s", d)
				}
			}
		})
	}
}

func TestOpenAIReqToGeminiGenerationConfig(t *testing.T) {
	tests := []struct {
		name                     string
		input                    *openaischema.ChatCompletionRequest
		expectedGenerationConfig *genai.GenerationConfig
		expectedErrMsg           string
	}{
		{
			name: "all fields set",
			input: &openaischema.ChatCompletionRequest{
				Temperature:      ptr.To(0.7),
				TopP:             ptr.To(0.9),
				Seed:             ptr.To(42),
				TopLogProbs:      ptr.To(3),
				LogProbs:         ptr.To(true),
				N:                ptr.To(2),
				MaxTokens:        ptr.To(int64(256)),
				PresencePenalty:  ptr.To(float32(1.1)),
				FrequencyPenalty: ptr.To(float32(0.5)),
				Stop:             []*string{ptr.To("stop1"), ptr.To("stop2")},
			},
			expectedGenerationConfig: &genai.GenerationConfig{
				Temperature:      ptr.To(float32(0.7)),
				TopP:             ptr.To(float32(0.9)),
				Seed:             ptr.To(int32(42)),
				Logprobs:         ptr.To(int32(3)),
				ResponseLogprobs: true,
				CandidateCount:   2,
				MaxOutputTokens:  256,
				PresencePenalty:  ptr.To(float32(1.1)),
				FrequencyPenalty: ptr.To(float32(0.5)),
				StopSequences:    []string{"stop1", "stop2"},
			},
		},
		{
			name:                     "minimal fields",
			input:                    &openaischema.ChatCompletionRequest{},
			expectedGenerationConfig: &genai.GenerationConfig{},
		},
		{
			name: "text",
			input: &openaischema.ChatCompletionRequest{
				ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
					OfText: &openai.ResponseFormatTextParam{
						Type: "text",
					},
				},
			},
			expectedGenerationConfig: &genai.GenerationConfig{ResponseMIMEType: "text/plain"},
		},
		{
			name: "json object",
			input: &openaischema.ChatCompletionRequest{
				ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
					OfJSONObject: &openai.ResponseFormatJSONObjectParam{
						Type: "jsonobject",
					},
				},
			},
			expectedGenerationConfig: &genai.GenerationConfig{ResponseMIMEType: "application/json"},
		},
		{
			name: "json schema (map)",
			input: &openaischema.ChatCompletionRequest{
				ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
					OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
						Type: "jsonschema",
						JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
							Schema: map[string]interface{}{
								"type": "string",
							},
						},
					},
				},
			},
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType:   "application/json",
				ResponseJsonSchema: map[string]any{"type": "string"},
			},
		},
		{
			name: "json schema (string)",
			input: &openaischema.ChatCompletionRequest{
				ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
					OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
						Type: "jsonschema",
						JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
							Schema: `{"type":"string"}`,
						},
					},
				},
			},
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType:   "application/json",
				ResponseJsonSchema: map[string]any{"type": "string"},
			},
		},
		{
			name: "json schema (invalid string)",
			input: &openaischema.ChatCompletionRequest{
				ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
					OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
						Type: "jsonschema",
						JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
							Schema: `{"type":`, // invalid JSON.
						},
					},
				},
			},
			expectedErrMsg: "invalid JSON schema string",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := openAIReqToGeminiGenerationConfig(tc.input)
			if tc.expectedErrMsg != "" {
				require.ErrorContains(t, err, tc.expectedErrMsg)
			} else {
				require.NoError(t, err)

				if diff := cmp.Diff(tc.expectedGenerationConfig, got, cmpopts.IgnoreUnexported(genai.GenerationConfig{})); diff != "" {
					t.Errorf("GenerationConfig mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestOpenAIToolsToGeminiTools(t *testing.T) {
	funcParams := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{"type": "integer"},
			"b": map[string]any{"type": "integer"},
		},
		"required": []any{"a", "b"},
	}
	tests := []struct {
		name          string
		openaiTools   []openai.ChatCompletionToolParam
		expected      []genai.Tool
		expectedError string
	}{
		{
			name:        "empty tools",
			openaiTools: nil,
			expected:    nil,
		},
		{
			name: "single function tool with parameters",
			openaiTools: []openai.ChatCompletionToolParam{
				{
					Type: openaischema.ToolTypeFunction,
					Function: openai.FunctionDefinitionParam{
						Name:        "add",
						Description: openai.Opt("Add two numbers"),
						Parameters:  funcParams,
					},
				},
			},
			expected: []genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Name:                 "add",
							Description:          "Add two numbers",
							ParametersJsonSchema: funcParams,
						},
					},
				},
			},
		},
		{
			name: "multiple function tools",
			openaiTools: []openai.ChatCompletionToolParam{
				{
					Type: openaischema.ToolTypeFunction,
					Function: openai.FunctionDefinitionParam{
						Name:        "foo",
						Description: openai.Opt("Foo function"),
					},
				},
				{
					Type: openaischema.ToolTypeFunction,
					Function: openai.FunctionDefinitionParam{
						Name:        "bar",
						Description: openai.Opt("Bar function"),
					},
				},
			},
			expected: []genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Name:                 "foo",
							Description:          "Foo function",
							Parameters:           nil,
							ParametersJsonSchema: nil,
						},
						{
							Name:                 "bar",
							Description:          "Bar function",
							Parameters:           nil,
							ParametersJsonSchema: nil,
						},
					},
				},
			},
		},
		{
			name: "tool with invalid parameters schema",
			openaiTools: []openai.ChatCompletionToolParam{
				{
					Type: openaischema.ToolTypeFunction,
					Function: openai.FunctionDefinitionParam{
						Name:        "bad",
						Description: openai.Opt("Bad function"),
						Parameters: openai.FunctionParameters{
							"FunctionParameters": "invalid-json",
						},
					},
				},
			},
			expected: []genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Description:          "Bad function",
							Name:                 "bad",
							ParametersJsonSchema: "invalid-json",
						},
					},
				},
			},
		},
		{
			name: "non-function tool is ignored",
			openaiTools: []openai.ChatCompletionToolParam{
				{
					Type: "retrieval",
				},
			},
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := openAIToolsToGeminiTools(tc.openaiTools)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				if d := cmp.Diff(tc.expected, result, cmpopts.IgnoreUnexported(genai.Schema{})); d != "" {
					t.Errorf("Result mismatch (-want +got):\n%s", d)
				}
			}
		})
	}
}

func TestOpenAIToolChoiceToGeminiToolConfig(t *testing.T) {
	tests := []struct {
		name      string
		input     openai.ChatCompletionToolChoiceOptionUnionParam
		expected  *genai.ToolConfig
		expectErr string
	}{
		{
			name:     "string auto",
			input:    openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.Opt("auto")},
			expected: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAuto}},
		},
		{
			name:     "string none",
			input:    openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.Opt("none")},
			expected: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeNone}},
		},
		{
			name:     "string required",
			input:    openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.Opt("required")},
			expected: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAny}},
		},
		{
			name: "ToolChoice struct",
			input: openai.ChatCompletionToolChoiceOptionUnionParam{
				OfChatCompletionNamedToolChoice: &openai.ChatCompletionNamedToolChoiceParam{
					Type:     openaischema.ToolTypeFunction,
					Function: openai.ChatCompletionNamedToolChoiceFunctionParam{Name: "myfunc"},
				},
			},
			expected: &genai.ToolConfig{
				FunctionCallingConfig: &genai.FunctionCallingConfig{
					Mode:                 genai.FunctionCallingConfigModeAny,
					AllowedFunctionNames: []string{"myfunc"},
				},
				RetrievalConfig: nil,
			},
		},
		{
			name:      "unsupported string value",
			input:     openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.Opt("invalid")},
			expectErr: "unsupported tool choice: 'invalid'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := openAIToolChoiceToGeminiToolConfig(tc.input)
			if tc.expectErr != "" {
				require.ErrorContains(t, err, tc.expectErr)
			} else {
				require.NoError(t, err)
				if diff := cmp.Diff(tc.expected, got, cmpopts.IgnoreUnexported(genai.ToolConfig{}, genai.FunctionCallingConfig{})); diff != "" {
					t.Errorf("ToolConfig mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}
