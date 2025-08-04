// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/openai/openai-go"
	openAIconstant "github.com/openai/openai-go/shared/constant"
	"io"
	"strconv"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	anthropicVertex "github.com/anthropics/anthropic-sdk-go/vertex"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"k8s.io/utils/ptr"

	aigwopenai "github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

const (
	claudeTestModel = "claude-3-opus-20240229"
	testTool        = "test_123"
)

func TestOpenAIToGCPAnthropicTranslatorV1ChatCompletion_RequestBody(t *testing.T) {
	// Define a common input request to use for both standard and vertex tests.
	openAIReq := &aigwopenai.ChatCompletionRequest{
		Model: claudeTestModel,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: openai.ChatCompletionSystemMessageParamContentUnion{OfString: openai.Opt("You are a helpful assistant.")}},
			},
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{OfString: openai.Opt("Hello!")}},
			},
		},
		MaxTokens:   ptr.To(int64(1024)),
		Temperature: ptr.To(0.7),
	}
	t.Run("Vertex Values Configured Correctly", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		hm, bm, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)
		require.NotNil(t, bm)

		// Check the path header.
		pathHeader := hm.SetHeaders[0]
		require.Equal(t, ":path", pathHeader.Header.Key)
		expectedPath := fmt.Sprintf("publishers/anthropic/models/%s:rawPredict", openAIReq.Model)
		require.Equal(t, expectedPath, string(pathHeader.Header.RawValue))

		// Check the body content.
		body := bm.GetBody()
		require.NotNil(t, body)
		// Model should NOT be present in the body for GCP Vertex.
		require.False(t, gjson.GetBytes(body, "model").Exists())
		// Anthropic version should be present for GCP Vertex.
		require.Equal(t, anthropicVertex.DefaultVersion, gjson.GetBytes(body, "anthropic_version").String())
	})

	t.Run("Model Name Override", func(t *testing.T) {
		overrideModelName := "claude-3"
		// Instantiate the translator with the model name override.
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", overrideModelName)

		// Call RequestBody with the original request, which has a different model name.
		hm, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)

		// Check that the :path header uses the override model name.
		pathHeader := hm.SetHeaders[0]
		require.Equal(t, ":path", pathHeader.Header.Key)
		expectedPath := fmt.Sprintf("publishers/anthropic/models/%s:rawPredict", overrideModelName)
		require.Equal(t, expectedPath, string(pathHeader.Header.RawValue))
	})

	t.Run("Image Content Request", func(t *testing.T) {
		imageReq := &aigwopenai.ChatCompletionRequest{
			MaxCompletionTokens: ptr.To(int64(200)),
			Model:               "claude-3-opus-20240229",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.ChatCompletionUserMessageParamContentUnion{
							OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
								{OfText: &openai.ChatCompletionContentPartTextParam{Text: "What is in this image?"}},
								{OfImageURL: &openai.ChatCompletionContentPartImageParam{
									ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
										URL: "data:image/jpeg;base64,dGVzdA==", // "test" in base64.
									},
								}},
							},
						},
					},
				},
			},
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, bm, err := translator.RequestBody(nil, imageReq, false)
		require.NoError(t, err)
		body := bm.GetBody()
		imageBlock := gjson.GetBytes(body, "messages.0.content.1")
		require.Equal(t, "image", imageBlock.Get("type").String())
		require.Equal(t, "base64", imageBlock.Get("source.type").String())
		require.Equal(t, "image/jpeg", imageBlock.Get("source.media_type").String())
		require.Equal(t, "dGVzdA==", imageBlock.Get("source.data").String())
	})

	t.Run("Multiple System Prompts Concatenated", func(t *testing.T) {
		firstMsg := "First system prompt."
		secondMsg := "Second developer prompt."
		thirdMsg := "Hello!"
		multiSystemReq := &aigwopenai.ChatCompletionRequest{
			Model: claudeTestModel,
			Messages: []openai.ChatCompletionMessageParamUnion{
				{OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: openai.ChatCompletionSystemMessageParamContentUnion{OfString: openai.Opt(firstMsg)}}},
				{OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
					Content: openai.ChatCompletionDeveloperMessageParamContentUnion{OfString: openai.Opt(secondMsg)}}},
				{OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{OfString: openai.Opt(thirdMsg)}}},
			},
			MaxTokens: ptr.To(int64(100)),
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, bm, err := translator.RequestBody(nil, multiSystemReq, false)
		require.NoError(t, err)
		body := bm.GetBody()
		require.Equal(t, firstMsg, gjson.GetBytes(body, "system.0.text").String())
		require.Equal(t, secondMsg, gjson.GetBytes(body, "system.1.text").String())
		require.Equal(t, thirdMsg, gjson.GetBytes(body, "messages.0.content.0.text").String())
	})

	t.Run("Streaming Request Error", func(t *testing.T) {
		streamReq := &aigwopenai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Stream:    true,
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, _, err := translator.RequestBody(nil, streamReq, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), errStreamingNotSupported.Error())
	})

	t.Run("Invalid Temperature (above bound)", func(t *testing.T) {
		invalidTempReq := &aigwopenai.ChatCompletionRequest{
			Model:       claudeTestModel,
			Messages:    []openai.ChatCompletionMessageParamUnion{},
			MaxTokens:   ptr.To(int64(100)),
			Temperature: ptr.To(2.5),
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, _, err := translator.RequestBody(nil, invalidTempReq, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), fmt.Sprintf(tempNotSupportedError, *invalidTempReq.Temperature))
	})

	t.Run("Invalid Temperature (below bound)", func(t *testing.T) {
		invalidTempReq := &aigwopenai.ChatCompletionRequest{
			Model:       claudeTestModel,
			Messages:    []openai.ChatCompletionMessageParamUnion{},
			MaxTokens:   ptr.To(int64(100)),
			Temperature: ptr.To(-2.5),
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, _, err := translator.RequestBody(nil, invalidTempReq, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), fmt.Sprintf(tempNotSupportedError, *invalidTempReq.Temperature))
	})

	// Test for missing required parameter.
	t.Run("Missing MaxTokens Throws Error", func(t *testing.T) {
		missingTokensReq := &aigwopenai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: nil,
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, _, err := translator.RequestBody(nil, missingTokensReq, false)
		require.ErrorContains(t, err, "the maximum number of tokens must be set for Anthropic, got nil instead")
	})
	t.Run("API Version Override", func(t *testing.T) {
		customAPIVersion := "bedrock-2023-05-31"
		// Instantiate the translator with the custom API version.
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator(customAPIVersion, "")

		// Call RequestBody with a standard request.
		_, bm, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, bm)

		// Check that the anthropic_version in the body uses the custom version.
		body := bm.GetBody()
		require.Equal(t, customAPIVersion, gjson.GetBytes(body, "anthropic_version").String())
	})
}

func TestOpenAIToGCPAnthropicTranslatorV1ChatCompletion_ResponseBody(t *testing.T) {
	t.Run("invalid json body", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, _, _, err := translator.ResponseBody(map[string]string{statusHeaderName: "200"}, bytes.NewBufferString("invalid json"), true)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to unmarshal body")
	})

	tests := []struct {
		name                   string
		inputResponse          *anthropic.Message
		respHeaders            map[string]string
		expectedOpenAIResponse aigwopenai.ChatCompletionResponse
	}{
		{
			name: "basic text response",
			inputResponse: &anthropic.Message{
				Role:       constant.Assistant(anthropic.MessageParamRoleAssistant),
				Content:    []anthropic.ContentBlockUnion{{Type: "text", Text: "Hello there!"}},
				StopReason: anthropic.StopReasonEndTurn,
				Usage:      anthropic.Usage{InputTokens: 10, OutputTokens: 20},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: aigwopenai.ChatCompletionResponse{
				Object: "chat.completion",
				Usage:  openai.CompletionUsage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
				Choices: []openai.ChatCompletionChoice{
					{
						Index:        0,
						Message:      openai.ChatCompletionMessage{Role: "assistant", Content: "Hello there!"},
						FinishReason: string(aigwopenai.ChatCompletionChoicesFinishReasonStop),
					},
				},
			},
		},
		{
			name: "response with tool use",
			inputResponse: &anthropic.Message{
				Role: constant.Assistant(anthropic.MessageParamRoleAssistant),
				Content: []anthropic.ContentBlockUnion{
					{Type: "text", Text: "Ok, I will call the tool."},
					{Type: "tool_use", ID: "toolu_01", Name: "get_weather", Input: json.RawMessage(`{"location": "Tokyo", "unit": "celsius"}`)},
				},
				StopReason: anthropic.StopReasonToolUse,
				Usage:      anthropic.Usage{InputTokens: 25, OutputTokens: 15},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: aigwopenai.ChatCompletionResponse{
				Object: "chat.completion",
				Usage:  openai.CompletionUsage{PromptTokens: 25, CompletionTokens: 15, TotalTokens: 40},
				Choices: []openai.ChatCompletionChoice{
					{
						Index:        0,
						FinishReason: string(aigwopenai.ChatCompletionChoicesFinishReasonToolCalls),
						Message: openai.ChatCompletionMessage{
							Role:    aigwopenai.ChatMessageRoleAssistant,
							Content: "Ok, I will call the tool.",
							ToolCalls: []openai.ChatCompletionMessageToolCall{
								{
									ID:   "toolu_01",
									Type: openAIconstant.ValueOf[openAIconstant.Function](),
									Function: openai.ChatCompletionMessageToolCallFunction{
										Name:      "get_weather",
										Arguments: `{"location":"Tokyo","unit":"celsius"}`,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.inputResponse)
			require.NoError(t, err, "Test setup failed: could not marshal input struct")

			translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
			hm, bm, usedToken, err := translator.ResponseBody(tt.respHeaders, bytes.NewBuffer(body), true)

			require.NoError(t, err, "Translator returned an unexpected internal error")
			require.NotNil(t, hm)
			require.NotNil(t, bm)

			newBody := bm.GetBody()
			require.NotNil(t, newBody)
			require.Len(t, hm.SetHeaders, 1)
			require.Equal(t, "content-length", hm.SetHeaders[0].Header.Key)
			require.Equal(t, strconv.Itoa(len(newBody)), string(hm.SetHeaders[0].Header.RawValue))

			var gotResp aigwopenai.ChatCompletionResponse
			err = json.Unmarshal(newBody, &gotResp)
			require.NoError(t, err)

			expectedTokenUsage := LLMTokenUsage{
				InputTokens:  uint32(tt.expectedOpenAIResponse.Usage.PromptTokens),     //nolint:gosec
				OutputTokens: uint32(tt.expectedOpenAIResponse.Usage.CompletionTokens), //nolint:gosec
				TotalTokens:  uint32(tt.expectedOpenAIResponse.Usage.TotalTokens),      //nolint:gosec
			}
			require.Equal(t, expectedTokenUsage, usedToken)

			if diff := cmp.Diff(tt.expectedOpenAIResponse, gotResp); diff != "" {
				t.Errorf("ResponseBody mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestMessageTranslation adds specific coverage for assistant and tool message translations.
func TestMessageTranslation(t *testing.T) {
	tests := []struct {
		name                  string
		inputMessages         []openai.ChatCompletionMessageParamUnion
		expectedAnthropicMsgs []anthropic.MessageParam
		expectedSystemBlocks  []anthropic.TextBlockParam
		expectErr             bool
	}{
		{
			name: "assistant message with text",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Content: openai.ChatCompletionAssistantMessageParamContentUnion{
							OfString: openai.Opt("Hello from the assistant."),
						},
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("Hello from the assistant.")},
				},
			},
		},
		{
			name: "assistant message with tool call",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								ID:       testTool,
								Type:     openAIconstant.ValueOf[openAIconstant.Function](),
								Function: openai.ChatCompletionMessageToolCallFunctionParam{Name: "get_weather", Arguments: `{"location":"NYC"}`},
							},
						},
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role: anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{
						{
							OfToolUse: &anthropic.ToolUseBlockParam{
								ID:    testTool,
								Type:  "tool_use",
								Name:  "get_weather",
								Input: map[string]interface{}{"location": "NYC"},
							},
						},
					},
				},
			},
		},
		{
			name: "assistant message with refusal",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Content: openai.ChatCompletionAssistantMessageParamContentUnion{
							OfArrayOfContentParts: []openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
								{
									OfRefusal: &openai.ChatCompletionContentPartRefusalParam{
										Refusal: "I cannot answer that.",
									},
								},
							},
						},
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("I cannot answer that.")},
				},
			},
		},
		{
			name: "tool message with text content",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfTool: &openai.ChatCompletionToolMessageParam{
						ToolCallID: testTool,
						Content: openai.ChatCompletionToolMessageParamContentUnion{
							OfString: openai.Opt("The weather is 72 degrees and sunny."),
						},
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role: anthropic.MessageParamRoleUser,
					Content: []anthropic.ContentBlockParamUnion{
						{
							OfToolResult: &anthropic.ToolResultBlockParam{
								ToolUseID: testTool,
								Type:      "tool_result",
								Content: []anthropic.ToolResultBlockParamContentUnion{
									{
										OfText: &anthropic.TextBlockParam{
											Text: "The weather is 72 degrees and sunny.",
											Type: "text",
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
			name: "system and developer messages",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfSystem: &openai.ChatCompletionSystemMessageParam{
						Content: openai.ChatCompletionSystemMessageParamContentUnion{
							OfString: openai.Opt("System prompt."),
						},
					},
				},
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.ChatCompletionUserMessageParamContentUnion{
							OfString: openai.Opt("User message."),
						},
					},
				},
				{
					OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
						Content: openai.ChatCompletionDeveloperMessageParamContentUnion{
							OfString: openai.Opt("Developer prompt."),
						},
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role:    anthropic.MessageParamRoleUser,
					Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("User message.")},
				},
			},
			expectedSystemBlocks: []anthropic.TextBlockParam{
				{Text: "System prompt."},
				{Text: "Developer prompt."},
			},
		},
		{
			name: "assistant message with tool call error",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								ID:       testTool,
								Type:     openAIconstant.ValueOf[openAIconstant.Function](),
								Function: openai.ChatCompletionMessageToolCallFunctionParam{Name: "get_weather", Arguments: `{"location":`},
							},
						},
					},
				},
			},
			expectErr: true,
		},
		{
			name: "multiple tool messages aggregated correctly",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfTool: &openai.ChatCompletionToolMessageParam{
						ToolCallID: "tool_1",
						Content:    openai.ChatCompletionToolMessageParamContentUnion{OfString: openai.Opt(`{"temp": "72F"}`)},
					},
				},
				{
					OfTool: &openai.ChatCompletionToolMessageParam{
						ToolCallID: "tool_2",
						Content:    openai.ChatCompletionToolMessageParamContentUnion{OfString: openai.Opt(`{"time": "16:00"}`)},
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role: anthropic.MessageParamRoleUser,
					Content: []anthropic.ContentBlockParamUnion{
						{
							OfToolResult: &anthropic.ToolResultBlockParam{
								ToolUseID: "tool_1",
								Type:      "tool_result",
								Content: []anthropic.ToolResultBlockParamContentUnion{
									{OfText: &anthropic.TextBlockParam{Text: `{"temp": "72F"}`, Type: "text"}},
								},
								IsError: anthropic.Bool(false),
							},
						},
						{
							OfToolResult: &anthropic.ToolResultBlockParam{
								ToolUseID: "tool_2",
								Type:      "tool_result",
								Content: []anthropic.ToolResultBlockParamContentUnion{
									{OfText: &anthropic.TextBlockParam{Text: `{"time": "16:00"}`, Type: "text"}},
								},
								IsError: anthropic.Bool(false),
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			openAIReq := &aigwopenai.ChatCompletionRequest{Messages: tt.inputMessages}
			anthropicMsgs, systemBlocks, err := openAIToAnthropicMessages(openAIReq.Messages)

			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				// Compare the conversational messages.
				require.Len(t, anthropicMsgs, len(tt.expectedAnthropicMsgs), "Number of translated messages should match")
				for i, expectedMsg := range tt.expectedAnthropicMsgs {
					actualMsg := anthropicMsgs[i]
					require.Equal(t, expectedMsg.Role, actualMsg.Role, "Message roles should match")
					require.Len(t, actualMsg.Content, len(expectedMsg.Content), "Number of content blocks should match")
					for j, expectedContent := range expectedMsg.Content {
						actualContent := actualMsg.Content[j]
						require.Equal(t, *expectedContent.GetType(), *actualContent.GetType(), "Content block types should match")
						if expectedContent.OfText != nil {
							require.NotNil(t, actualContent.OfText)
							require.Equal(t, expectedContent.OfText.Text, actualContent.OfText.Text)
						}
						if expectedContent.OfToolUse != nil {
							require.NotNil(t, actualContent.OfToolUse)
							require.Equal(t, expectedContent.OfToolUse.ID, actualContent.OfToolUse.ID)
							require.Equal(t, expectedContent.OfToolUse.Name, actualContent.OfToolUse.Name)
							require.Equal(t, expectedContent.OfToolUse.Input, actualContent.OfToolUse.Input)
						}
						if expectedContent.OfToolResult != nil {
							require.NotNil(t, actualContent.OfToolResult)
							require.Equal(t, expectedContent.OfToolResult.ToolUseID, actualContent.OfToolResult.ToolUseID)
							require.Len(t, actualContent.OfToolResult.Content, len(expectedContent.OfToolResult.Content))
							if expectedContent.OfToolResult.Content[0].OfText != nil {
								require.Equal(t, expectedContent.OfToolResult.Content[0].OfText.Text, actualContent.OfToolResult.Content[0].OfText.Text)
							}
							if expectedContent.OfToolResult.Content[0].OfImage != nil {
								require.NotNil(t, actualContent.OfToolResult.Content[0].OfImage, "Actual image block should not be nil")
								require.NotNil(t, actualContent.OfToolResult.Content[0].OfImage.Source, "Actual image source should not be nil")
								if expectedContent.OfToolResult.Content[0].OfImage.Source.OfBase64 != nil {
									require.NotNil(t, actualContent.OfToolResult.Content[0].OfImage.Source.OfBase64, "Actual base64 source should not be nil")
									require.Equal(t, expectedContent.OfToolResult.Content[0].OfImage.Source.OfBase64.Data, actualContent.OfToolResult.Content[0].OfImage.Source.OfBase64.Data)
								}
							}
						}
					}
				}

				// Compare the system prompt blocks.
				require.Len(t, systemBlocks, len(tt.expectedSystemBlocks), "Number of system blocks should match")
				for i, expectedBlock := range tt.expectedSystemBlocks {
					actualBlock := systemBlocks[i]
					require.Equal(t, expectedBlock.Text, actualBlock.Text, "System block text should match")
				}
			}
		})
	}
}

func TestOpenAIToGCPAnthropicTranslator_ResponseError(t *testing.T) {
	tests := []struct {
		name            string
		responseHeaders map[string]string
		inputBody       interface{}
		expectedOutput  aigwopenai.Error
	}{
		{
			name: "non-json error response",
			responseHeaders: map[string]string{
				statusHeaderName:      "503",
				contentTypeHeaderName: "text/plain; charset=utf-8",
			},
			inputBody: "Service Unavailable",
			expectedOutput: aigwopenai.Error{
				Type: "error",
				Error: aigwopenai.ErrorType{
					Type:    gcpBackendError,
					Code:    ptr.To("503"),
					Message: "Service Unavailable",
				},
			},
		},
		{
			name: "json error response",
			responseHeaders: map[string]string{
				statusHeaderName:      "400",
				contentTypeHeaderName: "application/json",
			},
			inputBody: &anthropic.ErrorResponse{
				Type: "error",
				Error: shared.ErrorObjectUnion{
					Type:    "invalid_request_error",
					Message: "Your max_tokens is too high.",
				},
			},
			expectedOutput: aigwopenai.Error{
				Type: "error",
				Error: aigwopenai.ErrorType{
					Type:    "invalid_request_error",
					Code:    ptr.To("400"),
					Message: "Your max_tokens is too high.",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reader io.Reader
			if bodyStr, ok := tt.inputBody.(string); ok {
				reader = bytes.NewBufferString(bodyStr)
			} else {
				bodyBytes, err := json.Marshal(tt.inputBody)
				require.NoError(t, err)
				reader = bytes.NewBuffer(bodyBytes)
			}

			o := &openAIToGCPAnthropicTranslatorV1ChatCompletion{}
			hm, bm, err := o.ResponseError(tt.responseHeaders, reader)

			require.NoError(t, err)
			require.NotNil(t, bm)
			require.NotNil(t, hm)

			newBody := bm.GetBody()
			require.NotNil(t, newBody)
			require.Len(t, hm.SetHeaders, 1)
			require.Equal(t, "content-length", hm.SetHeaders[0].Header.Key)
			require.Equal(t, strconv.Itoa(len(newBody)), string(hm.SetHeaders[0].Header.RawValue))

			var gotError openai.Error
			err = json.Unmarshal(newBody, &gotError)
			require.NoError(t, err)

			if diff := cmp.Diff(tt.expectedOutput, gotError); diff != "" {
				t.Errorf("ResponseError() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// New test function for helper coverage.
func TestHelperFunctions(t *testing.T) {
	t.Run("anthropicToOpenAIFinishReason invalid reason", func(t *testing.T) {
		_, err := anthropicToOpenAIFinishReason("unknown_reason")
		require.Error(t, err)
		require.Contains(t, err.Error(), "received invalid stop reason")
	})

	t.Run("anthropicRoleToOpenAIRole invalid role", func(t *testing.T) {
		_, err := anthropicRoleToOpenAIRole("unknown_role")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid anthropic role")
	})

	t.Run("process stop with nil", func(t *testing.T) {
		val, err := processStop(nil)
		require.NoError(t, err)
		require.Nil(t, val)
	})
}

func TestTranslateOpenAItoAnthropicTools(t *testing.T) {
	anthropicTestTool := []anthropic.ToolUnionParam{
		{OfTool: &anthropic.ToolParam{Name: "get_weather", Description: anthropic.String("")}},
	}
	openaiTestTool := []openai.ChatCompletionToolParam{
		{Type: "function", Function: openai.FunctionDefinitionParam{Name: "get_weather"}},
	}
	tests := []struct {
		name               string
		openAIReq          *aigwopenai.ChatCompletionRequest
		expectedTools      []anthropic.ToolUnionParam
		expectedToolChoice anthropic.ToolChoiceUnionParam
		expectErr          bool
	}{
		{
			name: "auto tool choice",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				ToolChoice: openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.Opt("auto")},
				Tools:      openaiTestTool,
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{
					DisableParallelToolUse: anthropic.Bool(false),
				},
			},
		},
		{
			name: "any tool choice",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				ToolChoice: openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.Opt("any")},
				Tools:      openaiTestTool,
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfAny: &anthropic.ToolChoiceAnyParam{},
			},
		},
		{
			name: "specific tool choice by name",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				ToolChoice: openai.ChatCompletionToolChoiceOptionUnionParam{
					OfChatCompletionNamedToolChoice: &openai.ChatCompletionNamedToolChoiceParam{
						Type: "function", Function: openai.ChatCompletionNamedToolChoiceFunctionParam{
							Name: "my_func",
						},
					},
				},
				Tools: openaiTestTool,
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfTool: &anthropic.ToolChoiceToolParam{Type: "function", Name: "my_func"},
			},
		},
		{
			name: "tool definition",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				Tools: []openai.ChatCompletionToolParam{
					{
						Type: "function",
						Function: openai.FunctionDefinitionParam{
							Name:        "get_weather",
							Description: openai.Opt("Get the weather"),
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"location": map[string]interface{}{"type": "string"},
								},
							},
						},
					},
				},
			},
			expectedTools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name:        "get_weather",
						Description: anthropic.String("Get the weather"),
						InputSchema: anthropic.ToolInputSchemaParam{
							Type: "object",
							Properties: map[string]interface{}{
								"location": map[string]interface{}{"type": "string"},
							},
						},
					},
				},
			},
		},
		{
			name: "tool_definition_with_required_field",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				Tools: []openai.ChatCompletionToolParam{
					{
						Type: "function",
						Function: openai.FunctionDefinitionParam{
							Name:        "get_weather",
							Description: openai.Opt("Get the weather with a required location"),
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"location": map[string]interface{}{"type": "string"},
									"unit":     map[string]interface{}{"type": "string"},
								},
								"required": []interface{}{"location"},
							},
						},
					},
				},
			},
			expectedTools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name:        "get_weather",
						Description: anthropic.String("Get the weather with a required location"),
						InputSchema: anthropic.ToolInputSchemaParam{
							Type: "object",
							Properties: map[string]interface{}{
								"location": map[string]interface{}{"type": "string"},
								"unit":     map[string]interface{}{"type": "string"},
							},
							Required: []string{"location"},
						},
					},
				},
			},
		},
		{
			name: "tool definition with no parameters",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				Tools: []openai.ChatCompletionToolParam{
					{
						Type: "function",
						Function: openai.FunctionDefinitionParam{
							Name:        "get_time",
							Description: openai.Opt("Get the current time"),
						},
					},
				},
			},
			expectedTools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name:        "get_time",
						Description: anthropic.String("Get the current time"),
					},
				},
			},
		},
		{
			name: "disable parallel tool calls",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				ToolChoice:        openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.Opt("auto")},
				Tools:             openaiTestTool,
				ParallelToolCalls: ptr.To(false),
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{
					DisableParallelToolUse: anthropic.Bool(true),
				},
			},
		},
		{
			name: "explicitly enable parallel tool calls",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				Tools:             openaiTestTool,
				ToolChoice:        openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.Opt("auto")},
				ParallelToolCalls: ptr.To(true),
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{DisableParallelToolUse: anthropic.Bool(false)},
			},
		},
		{
			name: "default disable parallel tool calls to false (nil)",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				Tools:      openaiTestTool,
				ToolChoice: openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.Opt("auto")},
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{DisableParallelToolUse: anthropic.Bool(false)},
			},
		},
		{
			name: "none tool choice",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				Tools:      openaiTestTool,
				ToolChoice: openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.Opt("none")},
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfNone: &anthropic.ToolChoiceNoneParam{},
			},
		},
		{
			name: "function tool choice",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				Tools:      openaiTestTool,
				ToolChoice: openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.Opt("function")},
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfTool: &anthropic.ToolChoiceToolParam{Name: "function"},
			},
		},
		{
			name: "invalid tool choice string",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				Tools:      openaiTestTool,
				ToolChoice: openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.Opt("invalid_choice")},
			},
			expectErr: true,
		},
		{
			name: "skips function tool with nil function definition",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				Tools: []openai.ChatCompletionToolParam{
					{
						Type:     "function",
						Function: openai.FunctionDefinitionParam{Name: "get_weather"}, // This is a valid tool.
					},
				},
			},
			// We expect only the valid function tool to be translated.
			expectedTools: []anthropic.ToolUnionParam{
				{OfTool: &anthropic.ToolParam{Name: "get_weather", Description: anthropic.String("")}},
			},
			expectErr: false,
		},
		{
			name: "skips non-function tools",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				Tools: []openai.ChatCompletionToolParam{
					{
						Type: "retrieval",
					},
					{
						Type:     "function",
						Function: openai.FunctionDefinitionParam{Name: "get_weather"},
					},
				},
			},
			expectedTools: []anthropic.ToolUnionParam{
				{OfTool: &anthropic.ToolParam{Name: "get_weather", Description: anthropic.String("")}},
			},
			expectErr: false,
		},
		{
			name: "tool definition without type field",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				Tools: []openai.ChatCompletionToolParam{
					{
						Type: "function",
						Function: openai.FunctionDefinitionParam{
							Name:        "get_weather",
							Description: openai.Opt("Get the weather without type"),
							Parameters: map[string]interface{}{
								"properties": map[string]interface{}{
									"location": map[string]interface{}{"type": "string"},
								},
								"required": []interface{}{"location"},
							},
						},
					},
				},
			},
			expectedTools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name:        "get_weather",
						Description: anthropic.String("Get the weather without type"),
						InputSchema: anthropic.ToolInputSchemaParam{
							Type: "",
							Properties: map[string]interface{}{
								"location": map[string]interface{}{"type": "string"},
							},
							Required: []string{"location"},
						},
					},
				},
			},
		},
		{
			name: "tool definition without properties field",
			openAIReq: &aigwopenai.ChatCompletionRequest{
				Tools: []openai.ChatCompletionToolParam{
					{
						Type: "function",
						Function: openai.FunctionDefinitionParam{
							Name:        "get_weather",
							Description: openai.Opt("Get the weather without properties"),
							Parameters: map[string]interface{}{
								"type":     "object",
								"required": []interface{}{"location"},
							},
						},
					},
				},
			},
			expectedTools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name:        "get_weather",
						Description: anthropic.String("Get the weather without properties"),
						InputSchema: anthropic.ToolInputSchemaParam{
							Type:     "object",
							Required: []string{"location"},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, toolChoice, err := translateOpenAItoAnthropicTools(tt.openAIReq.Tools, tt.openAIReq.ToolChoice, tt.openAIReq.ParallelToolCalls)
			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, toolChoice)
				require.Equal(t, *tt.expectedToolChoice.GetType(), *toolChoice.GetType())
				if tt.expectedToolChoice.GetName() != nil {
					require.Equal(t, *tt.expectedToolChoice.GetName(), *toolChoice.GetName())
				}
				if tt.expectedToolChoice.OfTool != nil {
					require.Equal(t, tt.expectedToolChoice.OfTool.Name, toolChoice.OfTool.Name)
				}
				if tt.expectedToolChoice.OfAuto != nil {
					require.Equal(t, tt.expectedToolChoice.OfAuto.DisableParallelToolUse, toolChoice.OfAuto.DisableParallelToolUse)
				}

				if tt.openAIReq.Tools != nil {
					require.NotNil(t, tools)
					require.Len(t, tools, len(tt.expectedTools))
					require.Equal(t, tt.expectedTools[0].GetName(), tools[0].GetName())
					require.Equal(t, tt.expectedTools[0].GetType(), tools[0].GetType())
					require.Equal(t, tt.expectedTools[0].GetDescription(), tools[0].GetDescription())
					if tt.expectedTools[0].GetInputSchema().Properties != nil {
						require.Equal(t, tt.expectedTools[0].GetInputSchema().Properties, tools[0].GetInputSchema().Properties)
					}
				}
			}
		})
	}
}

// TestFinishReasonTranslation covers specific cases for the anthropicToOpenAIFinishReason function.
func TestFinishReasonTranslation(t *testing.T) {
	tests := []struct {
		name                 string
		input                anthropic.StopReason
		expectedFinishReason aigwopenai.ChatCompletionChoicesFinishReason
		expectErr            bool
	}{
		{
			name:                 "max tokens stop reason",
			input:                anthropic.StopReasonMaxTokens,
			expectedFinishReason: aigwopenai.ChatCompletionChoicesFinishReasonLength,
		},
		{
			name:                 "refusal stop reason",
			input:                anthropic.StopReasonRefusal,
			expectedFinishReason: aigwopenai.ChatCompletionChoicesFinishReasonContentFilter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, err := anthropicToOpenAIFinishReason(tt.input)
			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedFinishReason, reason)
			}
		})
	}
}

// TestContentTranslationCoverage adds specific coverage for the openAIToAnthropicContent helper.
func TestContentTranslationCoverage(t *testing.T) {
	tests := []struct {
		name            string
		inputContent    openai.ChatCompletionUserMessageParamContentUnion
		expectedContent []anthropic.ContentBlockParamUnion
		expectErr       bool
	}{
		{
			name: "pdf data uri",
			inputContent: openai.ChatCompletionUserMessageParamContentUnion{
				OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
					{
						OfImageURL: &openai.ChatCompletionContentPartImageParam{
							ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
								URL: "data:application/pdf;base64,dGVzdA=="},
						},
					},
				},
			},
			expectedContent: []anthropic.ContentBlockParamUnion{
				{
					OfDocument: &anthropic.DocumentBlockParam{
						Source: anthropic.DocumentBlockParamSourceUnion{
							OfBase64: &anthropic.Base64PDFSourceParam{
								Type:      constant.ValueOf[constant.Base64](),
								MediaType: constant.ValueOf[constant.ApplicationPDF](),
								Data:      "dGVzdA==",
							},
						},
					},
				},
			},
		},
		{
			name: "pdf url",
			inputContent: openai.ChatCompletionUserMessageParamContentUnion{
				OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
					{
						OfImageURL: &openai.ChatCompletionContentPartImageParam{
							ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
								URL: "https://example.com/doc.pdf"}}},
				},
			},
			expectedContent: []anthropic.ContentBlockParamUnion{
				{
					OfDocument: &anthropic.DocumentBlockParam{
						Source: anthropic.DocumentBlockParamSourceUnion{
							OfURL: &anthropic.URLPDFSourceParam{
								Type: constant.ValueOf[constant.URL](),
								URL:  "https://example.com/doc.pdf",
							},
						},
					},
				},
			},
		},
		{
			name: "image url",
			inputContent: openai.ChatCompletionUserMessageParamContentUnion{
				OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
					{
						OfImageURL: &openai.ChatCompletionContentPartImageParam{
							ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
								URL: "https://example.com/image.png"},
						},
					},
				},
			},
			expectedContent: []anthropic.ContentBlockParamUnion{
				{
					OfImage: &anthropic.ImageBlockParam{
						Source: anthropic.ImageBlockParamSourceUnion{
							OfURL: &anthropic.URLImageSourceParam{
								Type: constant.ValueOf[constant.URL](),
								URL:  "https://example.com/image.png",
							},
						},
					},
				},
			},
		},
		{
			name: "audio content error",
			inputContent: openai.ChatCompletionUserMessageParamContentUnion{
				OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
					{
						OfInputAudio: &openai.ChatCompletionContentPartInputAudioParam{},
					},
				},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, err := openAIUserToAnthropicContent(tt.inputContent)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Use direct assertions instead of cmp.Diff to avoid panics on unexported fields.
			require.Len(t, content, len(tt.expectedContent), "Number of content blocks should match")

			// Use direct assertions instead of cmp.Diff to avoid panics on unexported fields.
			require.Len(t, content, len(tt.expectedContent), "Number of content blocks should match")
			for i, expectedBlock := range tt.expectedContent {
				actualBlock := content[i]
				require.Equal(t, expectedBlock.GetType(), actualBlock.GetType(), "Content block types should match")
				if expectedBlock.OfDocument != nil {
					require.NotNil(t, actualBlock.OfDocument, "Expected a document block, but got nil")
					require.NotNil(t, actualBlock.OfDocument.Source, "Document source should not be nil")

					if expectedBlock.OfDocument.Source.OfBase64 != nil {
						require.NotNil(t, actualBlock.OfDocument.Source.OfBase64, "Expected a base64 source")
						require.Equal(t, expectedBlock.OfDocument.Source.OfBase64.Data, actualBlock.OfDocument.Source.OfBase64.Data)
					}
					if expectedBlock.OfDocument.Source.OfURL != nil {
						require.NotNil(t, actualBlock.OfDocument.Source.OfURL, "Expected a URL source")
						require.Equal(t, expectedBlock.OfDocument.Source.OfURL.URL, actualBlock.OfDocument.Source.OfURL.URL)
					}
				}
				if expectedBlock.OfImage != nil {
					require.NotNil(t, actualBlock.OfImage, "Expected an image block, but got nil")
					require.NotNil(t, actualBlock.OfImage.Source, "Image source should not be nil")

					if expectedBlock.OfImage.Source.OfURL != nil {
						require.NotNil(t, actualBlock.OfImage.Source.OfURL, "Expected a URL image source")
						require.Equal(t, expectedBlock.OfImage.Source.OfURL.URL, actualBlock.OfImage.Source.OfURL.URL)
					}
				}
			}

			for i, expectedBlock := range tt.expectedContent {
				actualBlock := content[i]
				if expectedBlock.OfDocument != nil {
					require.NotNil(t, actualBlock.OfDocument, "Expected a document block, but got nil")
					require.NotNil(t, actualBlock.OfDocument.Source, "Document source should not be nil")

					if expectedBlock.OfDocument.Source.OfBase64 != nil {
						require.NotNil(t, actualBlock.OfDocument.Source.OfBase64, "Expected a base64 source")
						require.Equal(t, expectedBlock.OfDocument.Source.OfBase64.Data, actualBlock.OfDocument.Source.OfBase64.Data)
					}
					if expectedBlock.OfDocument.Source.OfURL != nil {
						require.NotNil(t, actualBlock.OfDocument.Source.OfURL, "Expected a URL source")
						require.Equal(t, expectedBlock.OfDocument.Source.OfURL.URL, actualBlock.OfDocument.Source.OfURL.URL)
					}
				}
				if expectedBlock.OfImage != nil {
					require.NotNil(t, actualBlock.OfImage, "Expected an image block, but got nil")
					require.NotNil(t, actualBlock.OfImage.Source, "Image source should not be nil")

					if expectedBlock.OfImage.Source.OfURL != nil {
						require.NotNil(t, actualBlock.OfImage.Source.OfURL, "Expected a URL image source")
						require.Equal(t, expectedBlock.OfImage.Source.OfURL.URL, actualBlock.OfImage.Source.OfURL.URL)
					}
				}
			}
		})
	}
}

// TestSystemPromptExtractionCoverage adds specific coverage for the extractSystemPromptFromDeveloperMsg helper.
func TestSystemPromptExtractionCoverage(t *testing.T) {
	tests := []struct {
		name           string
		inputMsg       openai.ChatCompletionDeveloperMessageParam
		expectedPrompt string
	}{
		{
			name: "developer message with content parts",
			inputMsg: openai.ChatCompletionDeveloperMessageParam{
				Content: openai.ChatCompletionDeveloperMessageParamContentUnion{
					OfArrayOfContentParts: []openai.ChatCompletionContentPartTextParam{
						{Text: "part 1"},
						{Text: "part 2"},
					}},
			},
			expectedPrompt: "part 1 part 2",
		},
		{
			name: "developer message with OfString",
			inputMsg: openai.ChatCompletionDeveloperMessageParam{
				Content: openai.ChatCompletionDeveloperMessageParamContentUnion{
					OfString: openai.Opt("nested string")},
			},
			expectedPrompt: "nested string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := extractSystemPromptFromDeveloperMsg(tt.inputMsg)
			require.Equal(t, tt.expectedPrompt, prompt)
		})
	}
}
