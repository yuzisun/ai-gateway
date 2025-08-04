// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared/constant"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
)

func TestOpenAIChatCompletionContentPartUserUnionParamUnmarshal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     []byte
		out    *openai.ChatCompletionContentPartUnionParam
		expErr string
	}{
		{
			name: "text",
			in: []byte(`{
"type": "text",
"text": "what do you see in this image"
}`),
			out: &openai.ChatCompletionContentPartUnionParam{
				OfText: &openai.ChatCompletionContentPartTextParam{
					Text: "what do you see in this image",
				},
			},
		},
		{
			name: "image url",
			in: []byte(`{
"type": "image_url",
"image_url": {"url": "https://example.com/image.jpg"}
}`),
			out: &openai.ChatCompletionContentPartUnionParam{
				OfImageURL: &openai.ChatCompletionContentPartImageParam{
					ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
						URL: "https://example.com/image.jpg",
					},
					Type: constant.ImageURL(ChatCompletionContentPartImageTypeImageURL),
				},
			},
		},
		{
			name: "input audio",
			in: []byte(`{
"type": "input_audio",
"input_audio": {"data": "somebinarydata"}
}`),
			out: &openai.ChatCompletionContentPartUnionParam{
				OfInputAudio: &openai.ChatCompletionContentPartInputAudioParam{
					Type: constant.InputAudio(ChatCompletionContentPartInputAudioTypeInputAudio),
					InputAudio: openai.ChatCompletionContentPartInputAudioInputAudioParam{
						Data: "somebinarydata",
					},
				},
			},
		},
		{
			name:   "type not exist",
			in:     []byte(`{}`),
			expErr: "apijson: was not able to find discriminated union variant",
		},
		{
			name: "unknown type",
			in: []byte(`{
"type": "unknown"
}`),
			expErr: "apijson: was not able to find discriminated union variant",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var contentPart openai.ChatCompletionContentPartUnionParam
			err := json.Unmarshal(tc.in, &contentPart)
			if tc.expErr != "" {
				require.ErrorContains(t, err, tc.expErr)
				return
			}
			require.NoError(t, err)
			/*opt := cmpopts.EquateComparable(openai.ChatCompletionContentPartUnionParam{})
			if !cmp.Equal(&contentPart, tc.out, opt) {
				t.Errorf("UnmarshalOpenAIRequest(), diff(got, expected) = %s\n", cmp.Diff(&contentPart, tc.out))
			}*/
		})
	}
}

func TestOpenAIChatCompletionMessageUnmarshal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     []byte
		out    *ChatCompletionRequest
		expErr string
	}{
		{
			name: "basic test",
			in: []byte(`{"model": "gpu-o4",
                        "messages": [
                         {"role": "system", "content": "you are a helpful assistant"},
                         {"role": "developer", "content": "you are a helpful dev assistant"},
                         {"role": "user", "content": "what do you see in this image"},
                         {"role": "tool", "content": "some tool", "tool_call_id": "123"},
                         {"role": "assistant", "content": "you are a helpful assistant"}
                    ]}
`),
			out: &ChatCompletionRequest{
				Model: "gpu-o4",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfSystem: &openai.ChatCompletionSystemMessageParam{
							Role: ChatMessageRoleSystem,
							Content: openai.ChatCompletionSystemMessageParamContentUnion{
								OfString: openai.Opt("you are a helpful assistant"),
							},
						},
					},
					{
						OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
							Role: ChatMessageRoleDeveloper,
							Content: openai.ChatCompletionDeveloperMessageParamContentUnion{
								OfString: openai.Opt("you are a helpful dev assistant"),
							},
						},
					},
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role: ChatMessageRoleUser,
							Content: openai.ChatCompletionUserMessageParamContentUnion{
								OfString: openai.Opt("what do you see in this image"),
							},
						},
					},
					{
						OfTool: &openai.ChatCompletionToolMessageParam{
							Role:       ChatMessageRoleTool,
							ToolCallID: "123",
							Content: openai.ChatCompletionToolMessageParamContentUnion{
								OfString: openai.Opt("some tool"),
							},
						},
					},
					{
						OfAssistant: &openai.ChatCompletionAssistantMessageParam{
							Role: ChatMessageRoleAssistant,
							Content: openai.ChatCompletionAssistantMessageParamContentUnion{
								OfString: openai.Opt("you are a helpful assistant"),
							},
						},
					},
				},
			},
		},
		{
			name: "assistant message string",
			in: []byte(`{"model": "gpu-o4",
                        "messages": [
                         {"role": "assistant", "content": "you are a helpful assistant"},
			                   {"role": "assistant", "content": [{"text": "you are a helpful assistant content", "type": "text"}]}
                    ]}
`),
			out: &ChatCompletionRequest{
				Model: "gpu-o4",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfAssistant: &openai.ChatCompletionAssistantMessageParam{
							Role: ChatMessageRoleAssistant,
							Content: openai.ChatCompletionAssistantMessageParamContentUnion{
								OfString: openai.Opt("you are a helpful assistant"),
							},
						},
					},
					{
						OfAssistant: &openai.ChatCompletionAssistantMessageParam{
							Role: ChatMessageRoleAssistant,
							Content: openai.ChatCompletionAssistantMessageParamContentUnion{
								OfArrayOfContentParts: []openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
									{
										OfText: &openai.ChatCompletionContentPartTextParam{
											Type: "text",
											Text: "you are a helpful assistant content",
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "content with array",
			in: []byte(`{"model": "gpu-o4",
                        "messages": [
                         {"role": "system", "content": [{"text": "you are a helpful assistant", "type": "text"}]},
                         {"role": "developer", "content": [{"text": "you are a helpful dev assistant", "type": "text"}]},
                         {"role": "user", "content": [{"text": "what do you see in this image", "type": "text"}]}]}`),
			out: &ChatCompletionRequest{
				Model: "gpu-o4",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfSystem: &openai.ChatCompletionSystemMessageParam{
							Role: ChatMessageRoleSystem,
							Content: openai.ChatCompletionSystemMessageParamContentUnion{
								OfArrayOfContentParts: []openai.ChatCompletionContentPartTextParam{
									{
										Text: "you are a helpful assistant",
										Type: "text",
									},
								},
							},
						},
					},
					{
						OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
							Role: ChatMessageRoleDeveloper,
							Content: openai.ChatCompletionDeveloperMessageParamContentUnion{
								OfArrayOfContentParts: []openai.ChatCompletionContentPartTextParam{
									{
										Text: "you are a helpful dev assistant",
										Type: "text",
									},
								},
							},
						},
					},
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role: ChatMessageRoleUser,
							Content: openai.ChatCompletionUserMessageParamContentUnion{
								OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
									{
										OfText: &openai.ChatCompletionContentPartTextParam{
											Text: "what do you see in this image", Type: "text",
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:   "no role",
			in:     []byte(`{"model": "gpu-o4","messages": [{}]}`),
			expErr: "apijson: was not able to find discriminated union variant",
		},
		{
			name: "unknown role",
			in: []byte(`{"model": "gpu-o4",
                        "messages": [{"role": "some-funky", "content": [{"text": "what do you see in this image", "type": "text"}]}]}`),
			expErr: "apijson: was not able to find discriminated union variant",
		},
		{
			name: "response_format",
			in:   []byte(`{ "model": "azure.gpt-4o", "messages": [ { "role": "user", "content": "Tell me a story" } ], "response_format": { "type": "json_schema", "json_schema": { "name": "math_response", "schema": { "type": "object", "properties": { "step": "test_step" }, "required": [ "steps"], "additionalProperties": false }, "strict": true } } }`),
			out: &ChatCompletionRequest{
				Model: "azure.gpt-4o",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role: ChatMessageRoleUser,
							Content: openai.ChatCompletionUserMessageParamContentUnion{
								OfString: openai.Opt("Tell me a story"),
							},
						},
					},
				},
				ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
					OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
						JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
							Name:   "math_response",
							Strict: openai.Opt(true),
							Schema: map[string]interface{}{
								"additionalProperties": false,
								"type":                 "object",
								"properties": map[string]interface{}{
									"step": "test_step",
								},
								"required": []interface{}{"steps"},
							},
						},
					},
				},
			},
		},
		{
			name: "test fields",
			in: []byte(`{
				"model": "gpu-o4",
				"messages": [{"role": "user", "content": "hello"}],
				"max_completion_tokens": 1024,
				"parallel_tool_calls": true,
				"stop": ["\n", "stop"],
				"service_tier": "flex"
			}`),
			out: &ChatCompletionRequest{
				Model: "gpu-o4",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role: ChatMessageRoleUser,
							Content: openai.ChatCompletionUserMessageParamContentUnion{
								OfString: openai.Opt("hello"),
							},
						},
					},
				},
				MaxCompletionTokens: ptr.To[int64](1024),
				ParallelToolCalls:   ptr.To(true),
				Stop:                []interface{}{"\n", "stop"},
				ServiceTier:         ptr.To("flex"),
			},
		},
		{
			name: "stop as string",
			in: []byte(`{
				"model": "gpu-o4",
				"messages": [{"role": "user", "content": "hello"}],
				"stop": "stop"
			}`),
			out: &ChatCompletionRequest{
				Model: "gpu-o4",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role: ChatMessageRoleUser,
							Content: openai.ChatCompletionUserMessageParamContentUnion{
								OfString: openai.Opt("hello"),
							},
						},
					},
				},
				Stop: "stop",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var chatCompletion ChatCompletionRequest
			err := json.Unmarshal(tc.in, &chatCompletion)
			if tc.expErr != "" {
				require.ErrorContains(t, err, tc.expErr)
				return
			}
			require.NoError(t, err)
			/*if !cmp.Equal(&chatCompletion, tc.out) {
				t.Errorf("UnmarshalOpenAIRequest(), diff(got, expected) = %s\n", cmp.Diff(&chatCompletion, tc.out))
			}*/
		})
	}
}

func TestModelListMarshal(t *testing.T) {
	var (
		model = Model{
			ID:      "gpt-3.5-turbo",
			Object:  "model",
			OwnedBy: "tetrate",
			Created: JSONUNIXTime(time.Date(2025, 0o1, 0o1, 0, 0, 0, 0, time.UTC)),
		}
		list = ModelList{Object: "list", Data: []Model{model}}
		raw  = `{"object":"list","data":[{"id":"gpt-3.5-turbo","object":"model","owned_by":"tetrate","created":1735689600}]}`
	)

	b, err := json.Marshal(list)
	require.NoError(t, err)
	require.JSONEq(t, raw, string(b))

	var out ModelList
	require.NoError(t, json.Unmarshal([]byte(raw), &out))
	require.Len(t, out.Data, 1)
	require.Equal(t, "list", out.Object)
	require.Equal(t, model.ID, out.Data[0].ID)
	require.Equal(t, model.Object, out.Data[0].Object)
	require.Equal(t, model.OwnedBy, out.Data[0].OwnedBy)
	// Unmarshalling initializes other fields in time.Time we're not interested with. Just compare the actual time.
	require.Equal(t, time.Time(model.Created).Unix(), time.Time(out.Data[0].Created).Unix())
}

func TestChatCompletionMessageParamUnionMarshal(t *testing.T) {
	testCases := []struct {
		name     string
		input    openai.ChatCompletionMessageParamUnion
		expected string
	}{
		{
			name: "user message",
			input: openai.ChatCompletionMessageParamUnion{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Role: ChatMessageRoleUser,
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfString: openai.Opt("Hello!"),
					},
				},
			},
			expected: `{"content":"Hello!","role":"user"}`,
		},
		{
			name: "system message",
			input: openai.ChatCompletionMessageParamUnion{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Role: ChatMessageRoleSystem,
					Content: openai.ChatCompletionSystemMessageParamContentUnion{
						OfString: openai.Opt("You are a helpful assistant"),
					},
				},
			},
			expected: `{"content":"You are a helpful assistant","role":"system"}`,
		},
		{
			name: "assistant message",
			input: openai.ChatCompletionMessageParamUnion{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Role: ChatMessageRoleAssistant,
					Content: openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: openai.Opt("I can help you with that"),
					},
				},
			},
			expected: `{"role":"assistant","content":"I can help you with that"}`,
		},
		{
			name: "tool message",
			input: openai.ChatCompletionMessageParamUnion{
				OfTool: &openai.ChatCompletionToolMessageParam{
					Role:       ChatMessageRoleTool,
					ToolCallID: "123",
					Content: openai.ChatCompletionToolMessageParamContentUnion{
						OfString: openai.Opt("tool result"),
					},
				},
			},
			expected: `{"content":"tool result","role":"tool","tool_call_id":"123"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := json.Marshal(tc.input)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(result))
		})
	}
}

func TestStringOrUserRoleContentUnionMarshal(t *testing.T) {
	testCases := []struct {
		name     string
		input    openai.ChatCompletionUserMessageParamContentUnion
		expected string
	}{
		{
			name: "string value",
			input: openai.ChatCompletionUserMessageParamContentUnion{
				OfString: openai.Opt("What is the weather?"),
			},
			expected: `"What is the weather?"`,
		},
		{
			name: "content array",
			input: openai.ChatCompletionUserMessageParamContentUnion{
				OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
					{
						OfText: &openai.ChatCompletionContentPartTextParam{
							Type: "text",
							Text: "What's in this image?",
						},
					},
				},
			},
			expected: `[{"text":"What's in this image?","type":"text"}]`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := json.Marshal(tc.input)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(result))
		})
	}
}

func TestStringOrAssistantRoleContentUnionMarshal(t *testing.T) {
	testCases := []struct {
		name     string
		input    openai.ChatCompletionAssistantMessageParamContentUnion
		expected string
	}{
		{
			name: "string value",
			input: openai.ChatCompletionAssistantMessageParamContentUnion{
				OfString: openai.Opt("I can help with that"),
			},
			expected: `"I can help with that"`,
		},
		{
			name: "content object",
			input: openai.ChatCompletionAssistantMessageParamContentUnion{
				OfArrayOfContentParts: []openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
					{
						OfText: &openai.ChatCompletionContentPartTextParam{
							Text: "Here is the answer",
							Type: "text",
						},
					},
				},
			},
			expected: `[{"type":"text","text":"Here is the answer"}]`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := json.Marshal(tc.input)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(result))
		})
	}
}

func TestChatCompletionContentPartUserUnionParamMarshal(t *testing.T) {
	testCases := []struct {
		name     string
		input    openai.ChatCompletionContentPartUnionParam
		expected string
	}{
		{
			name: "text content",
			input: openai.ChatCompletionContentPartUnionParam{
				OfText: &openai.ChatCompletionContentPartTextParam{
					Type: "text",
					Text: "Hello world",
				},
			},
			expected: `{"text":"Hello world","type":"text"}`,
		},
		{
			name: "image content",
			input: openai.ChatCompletionContentPartUnionParam{
				OfImageURL: &openai.ChatCompletionContentPartImageParam{
					Type: "image_url",
					ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
						URL: "https://example.com/image.jpg",
					},
				},
			},
			expected: `{"image_url":{"url":"https://example.com/image.jpg"},"type":"image_url"}`,
		},
		{
			name: "audio content",
			input: openai.ChatCompletionContentPartUnionParam{
				OfInputAudio: &openai.ChatCompletionContentPartInputAudioParam{
					Type: "input_audio",
					InputAudio: openai.ChatCompletionContentPartInputAudioInputAudioParam{
						Data: "audio-data",
					},
				},
			},
			expected: `{"input_audio":{"data":"audio-data"},"type":"input_audio"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := json.Marshal(tc.input)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(result))
		})
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	// Test that we can marshal and unmarshal a complete chat completion request
	req := &ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Role: ChatMessageRoleSystem,
					Content: openai.ChatCompletionSystemMessageParamContentUnion{
						OfString: openai.Opt("You are helpful"),
					},
				},
			},
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Role: ChatMessageRoleUser,
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
							{
								OfText: &openai.ChatCompletionContentPartTextParam{
									Type: "text",
									Text: "What's in this image?",
								},
							},
							{
								OfImageURL: &openai.ChatCompletionContentPartImageParam{
									Type: "image_url",
									ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
										URL: "https://example.com/image.jpg",
									},
								},
							},
						},
					},
				},
			},
		},
		Temperature: ptr.To(0.7),
		MaxTokens:   ptr.To[int64](100),
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(req)
	require.NoError(t, err)

	// Unmarshal back
	var decoded ChatCompletionRequest
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err)

	// Verify the structure
	require.Equal(t, req.Model, decoded.Model)
	require.Len(t, req.Messages, len(decoded.Messages))
	require.Equal(t, *req.Temperature, *decoded.Temperature)
	require.Equal(t, *req.MaxTokens, *decoded.MaxTokens)
}

func TestChatCompletionResponseUsageDetails(t *testing.T) {
	t.Run("with zero values omitted", func(t *testing.T) {
		// Test that zero values are omitted.
		usage := ChatCompletionResponseUsage{
			CompletionTokens: 9,
			PromptTokens:     19,
			TotalTokens:      28,
			CompletionTokensDetails: &CompletionTokensDetails{
				AcceptedPredictionTokens: 0,
				AudioTokens:              0,
				ReasoningTokens:          0,
				RejectedPredictionTokens: 0,
			},
			PromptTokensDetails: &PromptTokensDetails{
				AudioTokens:  0,
				CachedTokens: 0,
			},
		}

		// Marshal to JSON.
		jsonData, err := json.Marshal(usage)
		require.NoError(t, err)

		expected := `{"completion_tokens":9,"prompt_tokens":19,"total_tokens":28,"completion_tokens_details":{},"prompt_tokens_details":{}}`
		require.JSONEq(t, expected, string(jsonData))
	})

	t.Run("with non-zero values", func(t *testing.T) {
		// Test with actual non-zero values.
		usage := ChatCompletionResponseUsage{
			CompletionTokens: 11,
			PromptTokens:     37,
			TotalTokens:      48,
			CompletionTokensDetails: &CompletionTokensDetails{
				AcceptedPredictionTokens: 0,
				AudioTokens:              256,
				ReasoningTokens:          832,
				RejectedPredictionTokens: 0,
			},
			PromptTokensDetails: &PromptTokensDetails{
				AudioTokens:  8,
				CachedTokens: 384,
			},
		}

		// Marshal to JSON.
		jsonData, err := json.Marshal(usage)
		require.NoError(t, err)

		expected := `{
			"completion_tokens": 11,
			"prompt_tokens": 37,
			"total_tokens": 48,
			"completion_tokens_details": {
				"audio_tokens": 256,
				"reasoning_tokens": 832
			},
			"prompt_tokens_details": {
				"audio_tokens": 8,
				"cached_tokens": 384
			}
		}`
		require.JSONEq(t, expected, string(jsonData))

		// Unmarshal and verify.
		var decoded ChatCompletionResponseUsage
		err = json.Unmarshal(jsonData, &decoded)
		require.NoError(t, err)

		require.Equal(t, usage.CompletionTokens, decoded.CompletionTokens)
		require.Equal(t, usage.PromptTokens, decoded.PromptTokens)
		require.Equal(t, usage.TotalTokens, decoded.TotalTokens)
		require.NotNil(t, decoded.CompletionTokensDetails)
		require.Equal(t, 256, decoded.CompletionTokensDetails.AudioTokens)
		require.Equal(t, 832, decoded.CompletionTokensDetails.ReasoningTokens)
		require.NotNil(t, decoded.PromptTokensDetails)
		require.Equal(t, 8, decoded.PromptTokensDetails.AudioTokens)
		require.Equal(t, 384, decoded.PromptTokensDetails.CachedTokens)
	})
}

func TestChatCompletionResponseWithNewFields(t *testing.T) {
	// Test the new fields added to ChatCompletionResponse.
	resp := ChatCompletionResponse{
		ID:                "chatcmpl-test123",
		Created:           JSONUNIXTime(time.Now()),
		Model:             "gpt-4.1-nano",
		ServiceTier:       "default",
		SystemFingerprint: "",
		Object:            "chat.completion",
		Choices: []openai.ChatCompletionChoice{
			{
				Index:        0,
				FinishReason: string(ChatCompletionChoicesFinishReasonStop),
				Message: openai.ChatCompletionMessage{
					Role:    "assistant",
					Content: "Hello!",
				},
			},
		},
		Usage: openai.CompletionUsage{
			CompletionTokens: 1,
			PromptTokens:     5,
			TotalTokens:      6,
		},
	}

	// Marshal to JSON.
	jsonData, err := json.Marshal(resp)
	require.NoError(t, err)

	// Unmarshal back.
	var decoded ChatCompletionResponse
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err)

	// Verify all fields.
	require.Equal(t, resp.ID, decoded.ID)
	require.Equal(t, time.Time(resp.Created).Unix(), time.Time(decoded.Created).Unix())
	require.Equal(t, resp.Model, decoded.Model)
	require.Equal(t, resp.ServiceTier, decoded.ServiceTier)
	require.Equal(t, resp.SystemFingerprint, decoded.SystemFingerprint)
	require.Equal(t, resp.Object, decoded.Object)
}

func TestChatCompletionRequestModalities(t *testing.T) {
	// Test modalities field from OpenAI OpenAPI YAML examples.
	t.Run("text and audio modalities", func(t *testing.T) {
		jsonStr := `{
			"model": "gpt-4o-audio-preview",
			"messages": [{"role": "user", "content": "Hello!"}],
			"modalities": ["text", "audio"]
		}`

		var req ChatCompletionRequest
		err := json.Unmarshal([]byte(jsonStr), &req)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o-audio-preview", req.Model)
		require.Equal(t, []ChatCompletionModality{ChatCompletionModalityText, ChatCompletionModalityAudio}, req.Modalities)

		// Marshal back and verify.
		marshaled, err := json.Marshal(req)
		require.NoError(t, err)
		require.Contains(t, string(marshaled), `"modalities":["text","audio"]`)
	})

	t.Run("text only modality", func(t *testing.T) {
		jsonStr := `{
			"model": "gpt-4.1-nano",
			"messages": [{"role": "user", "content": "Hi"}],
			"modalities": ["text"]
		}`

		var req ChatCompletionRequest
		err := json.Unmarshal([]byte(jsonStr), &req)
		require.NoError(t, err)
		require.Equal(t, []ChatCompletionModality{ChatCompletionModalityText}, req.Modalities)
	})
}

func TestChatCompletionRequestAudio(t *testing.T) {
	// Test audio parameters from OpenAI OpenAPI YAML examples.
	t.Run("audio output parameters", func(t *testing.T) {
		jsonStr := `{
			"model": "gpt-4o-audio-preview",
			"messages": [{"role": "user", "content": "Hello!"}],
			"modalities": ["audio"],
			"audio": {
				"voice": "alloy",
				"format": "wav"
			}
		}`

		var req ChatCompletionRequest
		err := json.Unmarshal([]byte(jsonStr), &req)
		require.NoError(t, err)
		require.NotNil(t, req.Audio)
		require.Equal(t, ChatCompletionAudioVoiceAlloy, req.Audio.Voice)
		require.Equal(t, ChatCompletionAudioFormatWav, req.Audio.Format)

		// Marshal back and verify.
		marshaled, err := json.Marshal(req)
		require.NoError(t, err)
		require.Contains(t, string(marshaled), `"audio":{"voice":"alloy","format":"wav"}`)
	})

	t.Run("all audio formats", func(t *testing.T) {
		formats := []ChatCompletionAudioFormat{
			ChatCompletionAudioFormatWav,
			ChatCompletionAudioFormatAAC,
			ChatCompletionAudioFormatMP3,
			ChatCompletionAudioFormatFlac,
			ChatCompletionAudioFormatOpus,
			ChatCompletionAudioFormatPCM16,
		}

		for _, format := range formats {
			audio := ChatCompletionAudioParam{
				Voice:  ChatCompletionAudioVoiceNova,
				Format: format,
			}
			data, err := json.Marshal(audio)
			require.NoError(t, err)
			require.Contains(t, string(data), string(format))
		}
	})
}

func TestPredictionContent(t *testing.T) {
	// Test prediction content from OpenAI OpenAPI YAML examples.
	t.Run("prediction with string content", func(t *testing.T) {
		jsonStr := `{
			"model": "gpt-4.1-nano",
			"messages": [{"role": "user", "content": "Complete this: Hello"}],
			"prediction": {
				"type": "content",
				"content": "Hello world!"
			}
		}`

		var req ChatCompletionRequest
		err := json.Unmarshal([]byte(jsonStr), &req)
		require.NoError(t, err)
		require.NotNil(t, req.PredictionContent)
		require.Equal(t, PredictionContentTypeContent, req.PredictionContent.Type)
		require.Equal(t, "Hello world!", req.PredictionContent.Content.OfString.Value)

		// Marshal back and verify.
		marshaled, err := json.Marshal(req)
		require.NoError(t, err)
		require.Contains(t, string(marshaled), `"prediction":{"type":"content","content":"Hello world!"}`)
	})

	t.Run("prediction content type constant", func(t *testing.T) {
		// Verify the constant value matches OpenAPI spec.
		require.Equal(t, PredictionContentTypeContent, PredictionContentType("content"))
	})
}

func TestUnmarshalJSON_Unmarshal(t *testing.T) {
	jsonStr := `{"value": 3.14}`
	var data struct {
		Time JSONUNIXTime `json:"value"`
	}
	err := json.Unmarshal([]byte(jsonStr), &data)
	require.NoError(t, err)
	require.Equal(t, int64(3), time.Time(data.Time).Unix())

	jsonStr = `{"value": 2}`
	err = json.Unmarshal([]byte(jsonStr), &data)
	require.NoError(t, err)
	require.Equal(t, int64(2), time.Time(data.Time).Unix())
}
