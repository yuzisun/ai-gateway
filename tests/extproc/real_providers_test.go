// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_extproc

package extproc

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

// TestRealProviders tests the end-to-end flow of the external processor with Envoy and real providers.
func TestWithRealProviders(t *testing.T) {
	requireBinaries(t)
	accessLogPath := t.TempDir() + "/access.log"
	requireRunEnvoy(t, accessLogPath)
	configPath := t.TempDir() + "/extproc-config.yaml"

	cc := requireNewCredentialsContext(t)

	requireWriteFilterConfig(t, configPath, &filterapi.Config{
		MetadataNamespace: "ai_gateway_llm_ns",
		LLMRequestCosts: []filterapi.LLMRequestCost{
			{MetadataKey: "used_token", Type: filterapi.LLMRequestCostTypeInputToken},
			{MetadataKey: "some_cel", Type: filterapi.LLMRequestCostTypeCEL, CEL: "1+1"},
		},
		Schema: openAISchema,
		// This can be any header key, but it must match the envoy.yaml routing configuration.
		SelectedBackendHeaderKey: "x-selected-backend-name",
		ModelNameHeaderKey:       "x-model-name",
		Rules: []filterapi.RouteRule{
			{
				Backends: []filterapi.Backend{{Name: "openai", Schema: openAISchema, Auth: &filterapi.BackendAuth{
					APIKey: &filterapi.APIKeyAuth{Filename: cc.openAIAPIKeyFilePath},
				}}},
				Headers: []filterapi.HeaderMatch{{Name: "x-model-name", Value: "gpt-4o-mini"}},
			},
			{
				Backends: []filterapi.Backend{
					{Name: "aws-bedrock", Schema: awsBedrockSchema, Auth: &filterapi.BackendAuth{AWSAuth: &filterapi.AWSAuth{
						CredentialFileName: cc.awsFilePath,
						Region:             "eu-central-1",
						//Region:             "us-east-1",
					}}},
				},
				Headers: []filterapi.HeaderMatch{
					{Name: "x-model-name", Value: "eu.meta.llama3-2-1b-instruct-v1:0"},
					{Name: "x-model-name", Value: "eu.anthropic.claude-3-5-sonnet-20240620-v1:0"},
					//{Name: "x-model-name", Value: "us.meta.llama3-2-1b-instruct-v1:0"},
					//{Name: "x-model-name", Value: "us.anthropic.claude-3-5-sonnet-20240620-v1:0"},
				},
			},
		},
	})

	requireExtProc(t, os.Stdout, extProcExecutablePath(), configPath)

	//t.Run("health-checking", func(t *testing.T) {
	//	client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))
	//	for _, tc := range []realProvidersTestCase{
	//		{name: "openai", modelName: "gpt-4o-mini", required: requiredCredentialOpenAI},
	//		{name: "aws-bedrock", modelName: "eu.meta.llama3-2-1b-instruct-v1:0", required: requiredCredentialAWS},
	//		//{name: "aws-bedrock", modelName: "us.meta.llama3-2-1b-instruct-v1:0", required: requiredCredentialAWS},
	//	} {
	//		t.Run(tc.modelName, func(t *testing.T) {
	//			cc.maybeSkip(t, tc.required)
	//			require.Eventually(t, func() bool {
	//				chatCompletion, err := client.Chat.Completions.New(t.Context(), openai.ChatCompletionNewParams{
	//					Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
	//						openai.UserMessage("Say this is a test"),
	//					}),
	//					Model: openai.F(tc.modelName),
	//				})
	//				if err != nil {
	//					t.Logf("error: %v", err)
	//					return false
	//				}
	//				nonEmptyCompletion := false
	//				for _, choice := range chatCompletion.Choices {
	//					t.Logf("choice: %s", choice.Message.Content)
	//					if choice.Message.Content != "" {
	//						nonEmptyCompletion = true
	//					}
	//				}
	//				return nonEmptyCompletion
	//			}, 30*time.Second, 2*time.Second)
	//		})
	//	}
	//})
	//
	//// Read all access logs and check if the used token is logged.
	//// If the used token is set correctly in the metadata, it should be logged in the access log.
	//t.Run("check-used-token-metadata-access-log", func(t *testing.T) {
	//	cc.maybeSkip(t, requiredCredentialOpenAI|requiredCredentialAWS)
	//	// Since the access log might not be written immediately, we wait for the log to be written.
	//	require.Eventually(t, func() bool {
	//		accessLog, err := os.ReadFile(accessLogPath)
	//		require.NoError(t, err)
	//		// This should match the format of the access log in envoy.yaml.
	//		type lineFormat struct {
	//			UsedToken float64 `json:"used_token,omitempty"`
	//			SomeCel   float64 `json:"some_cel,omitempty"`
	//		}
	//		scanner := bufio.NewScanner(bytes.NewReader(accessLog))
	//		for scanner.Scan() {
	//			line := scanner.Bytes()
	//			var l lineFormat
	//			if err = json.Unmarshal(line, &l); err != nil {
	//				t.Logf("error unmarshalling line: %v", err)
	//				continue
	//			}
	//			t.Logf("line: %s", line)
	//			if l.SomeCel == 0 {
	//				t.Log("some_cel is not existent or greater than zero")
	//				continue
	//			}
	//			if l.UsedToken == 0 {
	//				t.Log("used_token is not existent or greater than zero")
	//				continue
	//			}
	//			return true
	//		}
	//		return false
	//	}, 30*time.Second, 2*time.Second)
	//})

	//t.Run("streaming", func(t *testing.T) {
	//	client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))
	//	for _, tc := range []realProvidersTestCase{
	//		{name: "openai", modelName: "gpt-4o-mini", required: requiredCredentialOpenAI},
	//		{name: "aws-bedrock", modelName: "us.meta.llama3-2-1b-instruct-v1:0", required: requiredCredentialAWS},
	//	} {
	//		t.Run(tc.name, func(t *testing.T) {
	//			cc.maybeSkip(t, tc.required)
	//			require.Eventually(t, func() bool {
	//				stream := client.Chat.Completions.NewStreaming(t.Context(), openai.ChatCompletionNewParams{
	//					Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
	//						openai.UserMessage("Say this is a test"),
	//					}),
	//					Model: openai.F(tc.modelName),
	//				})
	//				defer func() {
	//					_ = stream.Close()
	//				}()
	//
	//				acc := openai.ChatCompletionAccumulator{}
	//
	//				for stream.Next() {
	//					chunk := stream.Current()
	//					if !acc.AddChunk(chunk) {
	//						t.Log("error adding chunk")
	//						return false
	//					}
	//				}
	//
	//				if err := stream.Err(); err != nil {
	//					t.Logf("error: %v", err)
	//					return false
	//				}
	//
	//				nonEmptyCompletion := false
	//				for _, choice := range acc.Choices {
	//					t.Logf("choice: %s", choice.Message.Content)
	//					if choice.Message.Content != "" {
	//						nonEmptyCompletion = true
	//					}
	//				}
	//				if !nonEmptyCompletion {
	//					// Log the whole response for debugging.
	//					t.Logf("response: %+v", acc)
	//				}
	//				return nonEmptyCompletion
	//			}, 30*time.Second, 2*time.Second)
	//		})
	//	}
	//})

	t.Run("Bedrock uses tool in response", func(t *testing.T) {
		fmt.Println("starting tool test")
		client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))
		fmt.Println("after client")
		for _, tc := range []struct {
			testCaseName,
			modelName string
		}{
			{testCaseName: "aws-bedrock", modelName: "eu.anthropic.claude-3-5-sonnet-20240620-v1:0"}, // This will go to "aws-bedrock" using credentials file.
			//{testCaseName: "aws-bedrock", modelName: "us.anthropic.claude-3-5-sonnet-20240620-v1:0"}, // This will go to "aws-bedrock" using credentials file.
		} {
			t.Run(tc.modelName, func(t *testing.T) {
				fmt.Println("inside run")
				require.Eventually(t, func() bool {
					// Step 1: Initial tool call request
					question := "What is the weather in New York City?"
					params := openai.ChatCompletionNewParams{
						Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
							openai.UserMessage(question),
						}),
						Tools: openai.F([]openai.ChatCompletionToolParam{
							{
								Type: openai.F(openai.ChatCompletionToolTypeFunction),
								Function: openai.F(openai.FunctionDefinitionParam{
									Name:        openai.String("get_weather"),
									Description: openai.String("Get weather at the given location"),
									Parameters: openai.F(openai.FunctionParameters{
										"type": "object",
										"properties": map[string]interface{}{
											"location": map[string]string{
												"type": "string",
											},
										},
										"required": []string{"location"},
									}),
								}),
							},
						}),
						//// TODO: check if we should seed.
						//Seed:  openai.Int(0),
						Model: openai.F(tc.modelName),
					}
					completion, err := client.Chat.Completions.New(context.Background(), params)
					if err != nil {
						t.Logf("error: %v", err)
						return false
					}
					// Step 2: Verify tool call
					toolCalls := completion.Choices[0].Message.ToolCalls
					if len(toolCalls) == 0 {
						t.Logf("Expected tool call from completion result but got none")
						return false
					}
					// Step 3: Simulate the tool returning a response, add the tool response to the params, and check the second response
					params.Messages.Value = append(params.Messages.Value, completion.Choices[0].Message)
					t.Logf("appended param %+v\n", completion.Choices[0].Message)
					t.Logf("appended message content: %+v \n toolcalls: %+v\n", completion.Choices[0].Message.Content, completion.Choices[0].Message.ToolCalls)
					t.Logf("length of tool calls %v\n", len(completion.Choices[0].Message.ToolCalls))
					getWeatherCalled := false
					for _, toolCall := range toolCalls {
						t.Logf("tool id: %v", toolCall.ID)
						if toolCall.Function.Name == "get_weather" {
							getWeatherCalled = true
							// Extract the location from the function call arguments
							var args map[string]interface{}
							if argErr := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); argErr != nil {
								panic(argErr)
							}
							location := args["location"].(string)
							if location != "New York City" {
								t.Logf("Expected location to be New York City but got %s", location)
							}
							// Simulate getting weather data
							weatherData := "Sunny, 25°C"
							params.Messages.Value = append(params.Messages.Value, openai.ToolMessage(toolCall.ID, weatherData))
							t.Logf("Appended tool message: %v", openai.ToolMessage(toolCall.ID, weatherData)) // Debug log
						}
					}
					if getWeatherCalled == false {
						t.Logf("get_weather tool not specified in chat completion response")
						return false
					}

					for i, param := range params.Messages.Value {
						t.Logf("printing param message [%v] value: %v \n value ended \n", i, param)
					}

					secondChatCompletion, err := client.Chat.Completions.New(context.Background(), params)
					if err != nil {
						//TODO: remove
						t.Logf("secondChatCompletion w err: %v", secondChatCompletion)
						t.Logf("error during second response: %v", err)
						return false
					}

					// Step 4: Verify that the second response is correct
					completionResult := secondChatCompletion.Choices[0].Message.Content
					t.Logf("content of completion response using tool: %s", secondChatCompletion.Choices[0].Message.Content)
					return completionResult == "The weather in New York City is currently sunny and 25°C."
				}, 100*time.Second, 50*time.Second)
			})
		}
	})

	// Models are served by the extproc filter as a direct response so this can run even if the
	// real credentials are not present.
	// We don't need to run it on a concrete backend, as it will not route anywhere.
	//t.Run("list-models", func(t *testing.T) {
	//	client := openai.NewClient(option.WithBaseURL(listenerAddress + "/v1/"))
	//
	//	var models []string
	//
	//	require.EventuallyWithT(t, func(c *assert.CollectT) {
	//		it := client.Models.ListAutoPaging(t.Context())
	//		for it.Next() {
	//			models = append(models, it.Current().ID)
	//		}
	//		assert.NoError(c, it.Err())
	//	}, 30*time.Second, 2*time.Second)
	//	require.Equal(t, []string{
	//		"gpt-4o-mini",
	//		"eu.meta.llama3-2-1b-instruct-v1:0",
	//		"eu.anthropic.claude-3-5-sonnet-20240620-v1:0",
	//	}, models)
	//
	//	//require.Equal(t, []string{
	//	//	"gpt-4o-mini",
	//	//	"us.meta.llama3-2-1b-instruct-v1:0",
	//	//	"us.anthropic.claude-3-5-sonnet-20240620-v1:0",
	//	//}, models)
	//})
}

// realProvidersTestCase is a base test case for the real providers, which is mainly for the centralization of the
// credentials check.
type realProvidersTestCase struct {
	name      string
	modelName string
	required  requiredCredential
}

type requiredCredential byte

const (
	requiredCredentialOpenAI requiredCredential = 1 << iota
	requiredCredentialAWS
)

// credentialsContext holds the context for the credentials used in the tests.
type credentialsContext struct {
	openAIValid          bool
	awsValid             bool
	openAIAPIKeyFilePath string
	awsFilePath          string
}

// maybeSkip skips the test if the required credentials are not set.
func (c credentialsContext) maybeSkip(t *testing.T, required requiredCredential) {
	if required&requiredCredentialOpenAI != 0 && !c.openAIValid {
		t.Skip("skipping test as OpenAI API key is not set in TEST_OPENAI_API_KEY")
	}
	if required&requiredCredentialAWS != 0 && !c.awsValid {
		t.Skip("skipping test as AWS credentials are not set in TEST_AWS_ACCESS_KEY_ID and TEST_AWS_SECRET_ACCESS_KEY")
	}
}

// requireNewCredentialsContext creates a new credential context for the tests from the environment variables.
func requireNewCredentialsContext(t *testing.T) (ctx credentialsContext) {
	// Set up credential file for OpenAI.
	openAIAPIKey := os.Getenv("TEST_OPENAI_API_KEY")

	openAIAPIKeyFilePath := t.TempDir() + "/open-ai-api-key"
	file, err := os.Create(openAIAPIKeyFilePath)
	require.NoError(t, err)
	_, err = file.WriteString(cmp.Or(openAIAPIKey, "dummy-openai-api-key"))
	require.NoError(t, err)

	// Set up credential file for AWS.
	awsAccessKeyID := os.Getenv("TEST_AWS_ACCESS_KEY_ID")
	awsSecretAccessKey := os.Getenv("TEST_AWS_SECRET_ACCESS_KEY")
	awsSessionToken := os.Getenv("TEST_AWS_SESSION_TOKEN")
	var awsCredentialsBody string
	if awsSessionToken != "" {
		awsCredentialsBody = fmt.Sprintf("[default]\nAWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\nAWS_SESSION_TOKEN=%s\n",
			cmp.Or(awsAccessKeyID, "dummy_access_key_id"), cmp.Or(awsSecretAccessKey, "dummy_secret_access_key"), awsSessionToken)
	} else {
		awsCredentialsBody = fmt.Sprintf("[default]\nAWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\n",
			cmp.Or(awsAccessKeyID, "dummy_access_key_id"), cmp.Or(awsSecretAccessKey, "dummy_secret_access_key"))
	}
	awsFilePath := t.TempDir() + "/aws-credential-file"
	awsFile, err := os.Create(awsFilePath)
	require.NoError(t, err)
	defer func() { require.NoError(t, awsFile.Close()) }()
	_, err = awsFile.WriteString(awsCredentialsBody)
	require.NoError(t, err)

	return credentialsContext{
		openAIValid:          openAIAPIKey != "",
		awsValid:             awsAccessKeyID != "" && awsSecretAccessKey != "",
		openAIAPIKeyFilePath: openAIAPIKeyFilePath,
		awsFilePath:          awsFilePath,
	}
}
