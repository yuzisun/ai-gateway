// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"io"
	"path"
	"strconv"

	"github.com/anthropics/anthropic-sdk-go"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/tidwall/sjson"
)

// NewMessageAnthropicToAWSBedrockTranslator implements [Factory] for Anthropic to Anthropic translation.
func NewMessageAnthropicToAWSBedrockTranslator(apiVersion string, modelNameOverride string) AnthropicMessageTranslator {
	return &anthropicToAWSBedrockTranslatorMessage{
		modelNameOverride: modelNameOverride,
		path:              path.Join("/", apiVersion, "message"),
	}
}

// anthropicToAWSBedrockTranslatorMessage implements [Translator] for /chat/completions.
type anthropicToAWSBedrockTranslatorMessage struct {
	modelNameOverride string
	stream            bool
	buffered          []byte
	bufferingDone     bool
	// The path of the chat completions endpoint to be used for the request. It is prefixed with the OpenAI path prefix.
	path string
}

// RequestBody implements [AnthropicMessageTranslator.RequestBody].
func (o *anthropicToAWSBedrockTranslatorMessage) RequestBody(raw []byte, req *anthropic.MessageNewParams, onRetry bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	if val, ok := req.Metadata.ExtraFields()["stream"]; ok {
		o.stream = val.(bool)
	}
	var pathTemplate string
	if o.stream {
		o.stream = true
		pathTemplate = "/model/%s/converse-stream"
	} else {
		pathTemplate = "/model/%s/converse"
	}
	modelName := string(req.Model)
	var newBody []byte
	if o.modelNameOverride != "" {
		modelName = o.modelNameOverride
		// If modelName is set, we override the model to be used for the request.
		out, err := sjson.SetBytesOptions(raw, "model", o.modelNameOverride, &sjson.Options{
			Optimistic:     true,
			ReplaceInPlace: true,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set model name: %w", err)
		}
		newBody = out
	}

	// Always set the path header to the converse endpoint so that the request is routed correctly.
	headerMutation = &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{
				Key:      ":path",
				RawValue: []byte(fmt.Sprintf(pathTemplate, modelName)),
			}},
		},
	}

	if onRetry {
		// On retry, the body might have changed to a different provider's format.
		newBody = raw
	}

	if len(newBody) > 0 {
		bodyMutation = &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: newBody},
		}
		headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{Header: &corev3.HeaderValue{
			Key:      "content-length",
			RawValue: []byte(strconv.Itoa(len(newBody))),
		}})
	}
	return
}

// ResponseError implements [Translator.ResponseError]
// For Anthropic based backend we return the Anthropic error type as is.
// If connection fails, the error body is translated to an Anthropic error type for events such as HTTP 503 or 504.
func (o *anthropicToAWSBedrockTranslatorMessage) ResponseError(respHeaders map[string]string, body io.Reader) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	if v, ok := respHeaders[contentTypeHeaderName]; ok && v != jsonContentType {
		var errorResponse anthropic.ErrorResponse
		buf, err := io.ReadAll(body)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read error body: %w", err)
		}
		errorResponse = anthropic.ErrorResponse{
			Error: anthropic.ErrorObjectUnion{
				Message: string(buf),
			},
			Type: constant.Error(statusCode),
		}
		mut := &extprocv3.BodyMutation_Body{}
		mut.Body, err = json.Marshal(errorResponse)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal error body: %w", err)
		}
		headerMutation = &extprocv3.HeaderMutation{}
		setContentLength(headerMutation, mut.Body)
		return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, nil
	}
	return nil, nil, nil
}

// ResponseHeaders implements [Translator.ResponseHeaders].
func (o *anthropicToAWSBedrockTranslatorMessage) ResponseHeaders(map[string]string) (headerMutation *extprocv3.HeaderMutation, err error) {
	return nil, nil
}

// ResponseBody implements [Translator.ResponseBody].
func (o *anthropicToAWSBedrockTranslatorMessage) ResponseBody(respHeaders map[string]string, body io.Reader, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, err error,
) {
	if v, ok := respHeaders[statusHeaderName]; ok {
		if v, err := strconv.Atoi(v); err == nil {
			if !isGoodStatusCode(v) {
				headerMutation, bodyMutation, err = o.ResponseError(respHeaders, body)
				return headerMutation, bodyMutation, LLMTokenUsage{}, err
			}
		}
	}
	if o.stream {
		if !o.bufferingDone {
			buf, err := io.ReadAll(body)
			if err != nil {
				return nil, nil, tokenUsage, fmt.Errorf("failed to read body: %w", err)
			}
			o.buffered = append(o.buffered, buf...)
			tokenUsage = o.extractUsageFromBufferEvent()
		}
		return
	}
	var resp anthropic.Message
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, nil, tokenUsage, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	tokenUsage = LLMTokenUsage{
		InputTokens:              uint32(resp.Usage.InputTokens),                           //nolint:gosec
		OutputTokens:             uint32(resp.Usage.OutputTokens),                          //nolint:gosec
		TotalTokens:              uint32(resp.Usage.InputTokens + resp.Usage.OutputTokens), //nolint:gosec
		CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
	}
	return
}

// extractUsageFromBufferEvent extracts the token usage from the buffered event.
// Once the usage is extracted, it returns the number of tokens used, and bufferingDone is set to true.
func (o *anthropicToAWSBedrockTranslatorMessage) extractUsageFromBufferEvent() (tokenUsage LLMTokenUsage) {
	dataPrefix := []byte("data: ")

	for {
		i := bytes.IndexByte(o.buffered, '\n')
		if i == -1 {
			return
		}
		line := o.buffered[:i]
		o.buffered = o.buffered[i+1:]
		if !bytes.HasPrefix(line, dataPrefix) {
			continue
		}
		var event anthropic.MessageStreamEventUnion
		if err := json.Unmarshal(bytes.TrimPrefix(line, dataPrefix), &event); err != nil {
			continue
		}
		tokenUsage = LLMTokenUsage{
			InputTokens:              uint32(event.Usage.InputTokens),                            //nolint:gosec
			OutputTokens:             uint32(event.Usage.OutputTokens),                           //nolint:gosec
			TotalTokens:              uint32(event.Usage.InputTokens + event.Usage.OutputTokens), //nolint:gosec
			CacheCreationInputTokens: event.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     event.Usage.CacheReadInputTokens,
		}
		o.bufferingDone = true
		o.buffered = nil
		return
	}
}
