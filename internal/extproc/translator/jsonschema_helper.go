// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"google.golang.org/genai"
)

// Constants for JSON Schema processing
const (
	RefKey        = "$ref"
	DefKey        = "$defs"
	TypeKey       = "type"
	ItemsKey      = "items"
	PropertiesKey = "properties"
	AllOfKey      = "allOf"
	AnyOfKey      = "anyOf"
	NullableKey   = "nullable"
	NullType      = "null"
	RefPrefix     = "#"

	// Error messages
	errRefMustBeString      = "'$ref' value must be a string"
	errRefMustStartWithHash = "ref paths are expected to be URI fragments, meaning they should start with #"
	errAllOfMultipleValues  = "only one value for 'allOf' key is supported"
	errTypeListLength       = "if the value of type is a list, the length of the list must be 2"
	errTypeListRequirements = "if type is a list, it must contain one non-null type and 'null'"
)

// Cache for allowed schema fields to avoid repeated reflection
var (
	allowedSchemaFields     map[string]struct{}
	allowedSchemaFieldsOnce sync.Once
)

// JSONSchemaProcessor handles JSON schema operations with better error handling and performance
type JSONSchemaProcessor struct {
	processedRefs map[string]struct{}
}

// NewJSONSchemaProcessor creates a new processor instance
func NewJSONSchemaProcessor() *JSONSchemaProcessor {
	return &JSONSchemaProcessor{
		processedRefs: make(map[string]struct{}),
	}
}

// getCachedAllowedSchemaFields returns the cached allowed schema fields for genai.Schema.
// This avoids repeated reflection calls and improves performance.
func getCachedAllowedSchemaFields() map[string]struct{} {
	allowedSchemaFieldsOnce.Do(func() {
		allowedSchemaFields = computeAllowedSchemaFields()
	})
	return allowedSchemaFields
}

// computeAllowedSchemaFields uses reflection to get supported field names from genai.Schema
func computeAllowedSchemaFields() map[string]struct{} {
	fieldSet := make(map[string]struct{})
	var schema *genai.Schema
	t := reflect.TypeOf(schema).Elem()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldSet[field.Name] = struct{}{}
	}

	return fieldSet
}

// deepCopyMapStringAny creates a deep copy of a map[string]any to prevent mutations
func deepCopyMapStringAny(original map[string]any) map[string]any {
	if original == nil {
		return nil
	}

	copied := make(map[string]any, len(original))
	for key, value := range original {
		copied[key] = deepCopyAny(value)
	}
	return copied
}

// deepCopyAny creates a deep copy of any value
func deepCopyAny(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return deepCopyMapStringAny(v)
	case []any:
		copiedSlice := make([]any, len(v))
		for i, elem := range v {
			copiedSlice[i] = deepCopyAny(elem)
		}
		return copiedSlice
	default:
		// For primitive types (int, string, bool, etc.) and other value types,
		// direct assignment performs a copy.
		return value
	}
}

// validateRefPath validates that a reference path is properly formatted
func validateRefPath(path string) error {
	if path == "" {
		return fmt.Errorf("reference path cannot be empty")
	}

	components := strings.Split(path, "/")
	if len(components) == 0 || components[0] != RefPrefix {
		return fmt.Errorf(errRefMustStartWithHash)
	}

	return nil
}

// retrieveRef fetches a deeply-nested reference from a schema map with improved error handling
func retrieveRef(path string, schema map[string]any) (any, error) {
	if err := validateRefPath(path); err != nil {
		return nil, fmt.Errorf("invalid reference path '%s': %w", path, err)
	}

	if schema == nil {
		return nil, fmt.Errorf("schema cannot be nil when retrieving reference '%s'", path)
	}

	components := strings.Split(path, "/")
	current := schema

	for i, component := range components[1:] {
		val, exists := current[component]
		if !exists {
			return nil, fmt.Errorf("reference '%s' not found: component '%s' does not exist at path level %d",
				path, component, i+1)
		}

		// Safe type assertion with proper error handling
		nextMap, ok := val.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("reference '%s' not found: component '%s' at level %d is not a map (got %T)",
				path, component, i+1, val)
		}
		current = nextMap
	}

	// Create and return a deep copy to prevent mutation of the original schema
	return deepCopyAny(current), nil
}

// dereferenceHelper recursively dereferences JSON schema references with better error handling
func (p *JSONSchemaProcessor) dereferenceHelper(
	obj any,
	fullSchema map[string]any,
	skipKeys []string,
) (any, error) {
	// Handle dictionaries (maps)
	if dict, ok := obj.(map[string]any); ok {
		objOut := make(map[string]any, len(dict))

		for k, v := range dict {
			// Check if key should be skipped
			if shouldSkipKey(k, skipKeys) {
				objOut[k] = v
				continue
			}

			// Handle reference key "$ref"
			if k == RefKey {
				refPath, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf(errRefMustBeString+" (got %T)", v)
				}

				// Check for circular references
				if _, exists := p.processedRefs[refPath]; exists {
					continue // Skip to avoid circular references
				}
				p.processedRefs[refPath] = struct{}{}

				ref, err := retrieveRef(refPath, fullSchema)
				if err != nil {
					return nil, fmt.Errorf("failed to retrieve reference '%s': %w", refPath, err)
				}

				fullRef, err := p.dereferenceHelper(ref, fullSchema, skipKeys)
				if err != nil {
					return nil, fmt.Errorf("failed to dereference '%s': %w", refPath, err)
				}

				delete(p.processedRefs, refPath) // Clean up processed refs
				return fullRef, nil
			}

			// Recurse on nested structures
			if nestedValue, err := p.processNestedValue(v, fullSchema, skipKeys); err != nil {
				return nil, fmt.Errorf("failed to process key '%s': %w", k, err)
			} else {
				objOut[k] = nestedValue
			}
		}
		return objOut, nil
	}

	// Handle lists (slices)
	if list, ok := obj.([]any); ok {
		listOut := make([]any, len(list))
		for i, el := range list {
			result, err := p.dereferenceHelper(el, fullSchema, skipKeys)
			if err != nil {
				return nil, fmt.Errorf("failed to process list element at index %d: %w", i, err)
			}
			listOut[i] = result
		}
		return listOut, nil
	}

	// Return non-dictionary and non-list types as is
	return obj, nil
}

// processNestedValue handles processing of nested values with appropriate type checking
func (p *JSONSchemaProcessor) processNestedValue(v any, fullSchema map[string]any, skipKeys []string) (any, error) {
	switch value := v.(type) {
	case map[string]any:
		return p.dereferenceHelper(value, fullSchema, skipKeys)
	case []any:
		return p.dereferenceHelper(value, fullSchema, skipKeys)
	default:
		return value, nil
	}
}

// shouldSkipKey checks if a key should be skipped during processing
func shouldSkipKey(key string, skipKeys []string) bool {
	for _, skipKey := range skipKeys {
		if key == skipKey {
			return true
		}
	}
	return false
}

// collectSkipKeys recursively traverses a schema to find keys that should be skipped
func collectSkipKeys(
	obj any,
	fullSchema map[string]any,
	processedRefs map[string]struct{},
) ([]string, error) {
	if processedRefs == nil {
		processedRefs = make(map[string]struct{})
	}

	var keys []string

	switch value := obj.(type) {
	case map[string]any:
		for k, v := range value {
			if k == RefKey {
				refPath, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf(errRefMustBeString+" (got %T)", v)
				}

				// Skip if reference has already been processed
				if _, exists := processedRefs[refPath]; exists {
					continue
				}
				processedRefs[refPath] = struct{}{}

				ref, err := retrieveRef(refPath, fullSchema)
				if err != nil {
					return nil, fmt.Errorf("failed to retrieve reference for skip analysis: %w", err)
				}

				// Add the top-level key of the reference to the list
				components := strings.Split(refPath, "/")
				if len(components) > 1 {
					keys = append(keys, components[1])
				}

				// Recurse on the referenced schema
				nestedKeys, err := collectSkipKeys(ref, fullSchema, processedRefs)
				if err != nil {
					return nil, err
				}
				keys = append(keys, nestedKeys...)
			} else {
				nestedKeys, err := collectSkipKeys(v, fullSchema, processedRefs)
				if err != nil {
					return nil, err
				}
				keys = append(keys, nestedKeys...)
			}
		}
	case []any:
		for i, el := range value {
			nestedKeys, err := collectSkipKeys(el, fullSchema, processedRefs)
			if err != nil {
				return nil, fmt.Errorf("failed to collect skip keys from list element %d: %w", i, err)
			}
			keys = append(keys, nestedKeys...)
		}
	}

	return keys, nil
}

// dereferenceSchema substitutes $refs in a JSON Schema object with improved error handling
func dereferenceSchema(schemaObj map[string]any) (any, error) {
	if schemaObj == nil {
		return nil, fmt.Errorf("schema object cannot be nil")
	}

	skipKeys, err := collectSkipKeys(schemaObj, schemaObj, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to collect skip keys: %w", err)
	}

	processor := NewJSONSchemaProcessor()
	return processor.dereferenceHelper(schemaObj, schemaObj, skipKeys)
}

// convertNullableType handles conversion of nullable types in JSON schemas
func convertNullableType(typeList []any, allowedSchemaFieldsSet map[string]struct{}) (map[string]any, error) {
	if len(typeList) != 2 {
		return nil, fmt.Errorf(errTypeListLength+" got %d", len(typeList))
	}

	var hasNull bool
	var nonNullType any

	for _, t := range typeList {
		if t == NullType {
			hasNull = true
		} else {
			nonNullType = t
		}
	}

	if !hasNull || nonNullType == nil {
		return nil, fmt.Errorf(errTypeListRequirements)
	}

	result := make(map[string]any)

	if nonNullTypeMap, ok := nonNullType.(map[string]any); ok {
		converted, err := convertToGAPIC(nonNullTypeMap, allowedSchemaFieldsSet)
		if err != nil {
			return nil, fmt.Errorf("failed to convert non-null type: %w", err)
		}
		for key, val := range converted {
			result[key] = val
		}
	} else {
		result[TypeKey] = fmt.Sprintf("%v", nonNullType)
	}

	result[NullableKey] = true
	return result, nil
}

// processAllOfSchema handles allOf schema processing
func processAllOfSchema(allOfList []any, allowedSchemaFieldsSet map[string]struct{}) (map[string]any, error) {
	if len(allOfList) > 1 {
		return nil, fmt.Errorf(errAllOfMultipleValues+" got %d", len(allOfList))
	}

	if len(allOfList) == 0 {
		return make(map[string]any), nil
	}

	subSchema, ok := allOfList[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("allOf element must be a map[string]any, got %T", allOfList[0])
	}

	return convertToGAPIC(subSchema, allowedSchemaFieldsSet)
}

// processAnyOfSchema handles anyOf schema processing
func processAnyOfSchema(anyOfList []any, allowedSchemaFieldsSet map[string]struct{}) (map[string]any, error) {
	result := make(map[string]any)
	anyOfResults := make([]any, 0, len(anyOfList))
	nullable := false

	for i, v := range anyOfList {
		subSchema, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("anyOf element at index %d must be a map[string]any, got %T", i, v)
		}

		if t, exists := subSchema[TypeKey]; exists && t == NullType {
			nullable = true
		} else {
			converted, err := convertToGAPIC(subSchema, allowedSchemaFieldsSet)
			if err != nil {
				return nil, fmt.Errorf("failed to convert anyOf element at index %d: %w", i, err)
			}
			anyOfResults = append(anyOfResults, converted)
		}
	}

	if nullable {
		result[NullableKey] = true
	}
	result[AnyOfKey] = anyOfResults
	return result, nil
}

// convertToGAPIC formats a JSON schema for a GAPIC request with improved error handling
func convertToGAPIC(schema map[string]any, allowedSchemaFieldsSet map[string]struct{}) (map[string]any, error) {
	if schema == nil {
		return nil, fmt.Errorf("schema cannot be nil")
	}

	convertedSchema := make(map[string]any, len(schema))

	for key, value := range schema {
		switch key {
		case DefKey:
			// Skip $defs
			continue

		case ItemsKey:
			if subSchema, ok := value.(map[string]any); ok {
				converted, err := convertToGAPIC(subSchema, allowedSchemaFieldsSet)
				if err != nil {
					return nil, fmt.Errorf("failed to convert items schema: %w", err)
				}
				convertedSchema[ItemsKey] = converted
			}

		case PropertiesKey:
			if properties, ok := value.(map[string]any); ok {
				convertedProperties := make(map[string]any, len(properties))
				for propKey, propValue := range properties {
					if propSubSchema, ok := propValue.(map[string]any); ok {
						converted, err := convertToGAPIC(propSubSchema, allowedSchemaFieldsSet)
						if err != nil {
							return nil, fmt.Errorf("failed to convert property '%s': %w", propKey, err)
						}
						convertedProperties[propKey] = converted
					} else {
						// Property value must be a schema object (map[string]any)
						return nil, fmt.Errorf("property '%s' must be a schema object, got %T", propKey, propValue)
					}
				}
				convertedSchema[PropertiesKey] = convertedProperties
			}

		case TypeKey:
			if typeList, ok := value.([]any); ok {
				converted, err := convertNullableType(typeList, allowedSchemaFieldsSet)
				if err != nil {
					return nil, fmt.Errorf("failed to convert nullable type: %w", err)
				}
				for k, v := range converted {
					convertedSchema[k] = v
				}
			} else {
				convertedSchema[TypeKey] = fmt.Sprintf("%v", value)
			}

		case AllOfKey:
			if allOfList, ok := value.([]any); ok {
				return processAllOfSchema(allOfList, allowedSchemaFieldsSet)
			}

		case AnyOfKey:
			if anyOfList, ok := value.([]any); ok {
				converted, err := processAnyOfSchema(anyOfList, allowedSchemaFieldsSet)
				if err != nil {
					return nil, err
				}
				for k, v := range converted {
					convertedSchema[k] = v
				}
			}

		default:
			// Check if the key is in the allowed set
			if _, allowed := allowedSchemaFieldsSet[key]; allowed {
				convertedSchema[key] = value
			}
		}
	}

	return convertedSchema, nil
}

// mapToSchema converts a map[string]any to a genai.Schema struct with better error handling
func mapToSchema(schemaMap map[string]any) (*genai.Schema, error) {
	if schemaMap == nil {
		return nil, fmt.Errorf("schema map cannot be nil")
	}

	jsonBytes, err := json.Marshal(schemaMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal schema map to JSON: %w", err)
	}

	var genSchema genai.Schema
	if err := json.Unmarshal(jsonBytes, &genSchema); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON to genai.Schema: %w", err)
	}

	return &genSchema, nil
}

// ConvertJSONSchemaToGemini converts a JSON schema to Gemini's genai.Schema format.
// This is the main entry point for JSON schema conversion with comprehensive validation and error handling.
//
// The function performs the following transformations:
// 1. Validates the input schema
// 2. Dereferences all $ref pointers in the schema
// 3. Filters fields to only those supported by genai.Schema
// 4. Converts to GAPIC-compatible format
// 5. Marshals to the final genai.Schema struct
//
// The function is used to convert OpenAI-style JSON schemas to Gemini's
// expected format for structured output generation.
func ConvertJSONSchemaToGemini(schema map[string]any) (*genai.Schema, error) {
	// Input validation
	if schema == nil {
		return nil, fmt.Errorf("ConvertJSONSchemaToGemini: input schema cannot be nil")
	}

	if len(schema) == 0 {
		return nil, fmt.Errorf("ConvertJSONSchemaToGemini: input schema cannot be empty")
	}

	// Step 1: Dereference the schema
	dereferencedSchema, err := dereferenceSchema(schema)
	if err != nil {
		return nil, fmt.Errorf("ConvertJSONSchemaToGemini: failed to dereference schema: %w", err)
	}

	// Type assertion with better error message
	dereferencedMap, ok := dereferencedSchema.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ConvertJSONSchemaToGemini: dereferenced schema is not a map[string]any, got %T", dereferencedSchema)
	}

	// Step 2: Get cached allowed fields (performance optimization)
	allowedSchemaFieldsSet := getCachedAllowedSchemaFields()

	// Step 3: Convert to GAPIC format
	schemaMap, err := convertToGAPIC(dereferencedMap, allowedSchemaFieldsSet)
	if err != nil {
		return nil, fmt.Errorf("ConvertJSONSchemaToGemini: failed to convert to GAPIC format: %w", err)
	}

	// Step 4: Convert to genai.Schema and return
	result, err := mapToSchema(schemaMap)
	if err != nil {
		return nil, fmt.Errorf("ConvertJSONSchemaToGemini: failed to convert to genai.Schema: %w", err)
	}

	return result, nil
}
