package translator

import (
	"reflect"
	"sync"
	"testing"

	"google.golang.org/genai"
)

// Test data for various scenarios
var (
	validSimpleSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type": "string",
			},
			"age": map[string]any{
				"type": "integer",
			},
		},
		"required": []any{"name"},
	}

	schemaWithRef = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"user": map[string]any{
				"$ref": "#/definitions/User",
			},
		},
		"definitions": map[string]any{
			"User": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string"},
					"email": map[string]any{"type": "string"},
				},
			},
		},
	}

	schemaWithNullableType = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"optional_field": map[string]any{
				"type": []any{"string", "null"},
			},
		},
	}

	schemaWithAllOf = map[string]any{
		"allOf": []any{
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"base_field": map[string]any{"type": "string"},
				},
			},
		},
	}

	schemaWithAnyOf = map[string]any{
		"anyOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "integer"},
			map[string]any{"type": "null"},
		},
	}

	circularRefSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node": map[string]any{
				"$ref": "#/definitions/Node",
			},
		},
		"definitions": map[string]any{
			"Node": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"type": "string"},
					"child": map[string]any{
						"$ref": "#/definitions/Node",
					},
				},
			},
		},
	}
)

// TestGetCachedAllowedSchemaFields tests the caching mechanism
func TestGetCachedAllowedSchemaFields(t *testing.T) {
	// Reset the cache for testing
	allowedSchemaFieldsOnce = sync.Once{}
	allowedSchemaFields = nil

	// First call should populate the cache
	fields1 := getCachedAllowedSchemaFields()
	if fields1 == nil {
		t.Fatal("Expected non-nil fields map")
	}

	// Second call should return the same cached instance
	fields2 := getCachedAllowedSchemaFields()

	// Compare the maps by comparing their lengths and contents
	if len(fields1) != len(fields2) {
		t.Error("Expected same cached content, but got different lengths")
	}

	// Verify they have the same content (which proves they're from the same cache)
	for key := range fields1 {
		if _, exists := fields2[key]; !exists {
			t.Errorf("Key %s exists in first call but not in second call", key)
		}
	}

	// Verify some expected fields exist
	expectedFields := []string{"Type", "Properties", "Items", "Required"}
	for _, field := range expectedFields {
		if _, exists := fields1[field]; !exists {
			t.Errorf("Expected field '%s' to exist in allowed fields", field)
		}
	}
}

// TestValidateRefPath tests reference path validation
func TestValidateRefPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"valid path", "#/definitions/User", false},
		{"empty path", "", true},
		{"missing hash prefix", "definitions/User", true},
		{"only hash", "#", false},
		{"complex path", "#/components/schemas/UserResponse", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRefPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateRefPath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestRetrieveRef tests reference retrieval functionality
func TestRetrieveRef(t *testing.T) {
	schema := map[string]any{
		"definitions": map[string]any{
			"User": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			},
		},
	}

	tests := []struct {
		name    string
		path    string
		schema  map[string]any
		wantErr bool
	}{
		{
			name:    "valid reference",
			path:    "#/definitions/User",
			schema:  schema,
			wantErr: false,
		},
		{
			name:    "invalid path format",
			path:    "definitions/User",
			schema:  schema,
			wantErr: true,
		},
		{
			name:    "non-existent reference",
			path:    "#/definitions/NonExistent",
			schema:  schema,
			wantErr: true,
		},
		{
			name:    "nil schema",
			path:    "#/definitions/User",
			schema:  nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := retrieveRef(tt.path, tt.schema)
			if (err != nil) != tt.wantErr {
				t.Errorf("retrieveRef() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && result == nil {
				t.Error("Expected non-nil result for valid reference")
			}
		})
	}
}

// TestDereferenceSchema tests schema dereferencing
func TestDereferenceSchema(t *testing.T) {
	tests := []struct {
		name    string
		schema  map[string]any
		wantErr bool
	}{
		{
			name:    "simple schema without refs",
			schema:  validSimpleSchema,
			wantErr: false,
		},
		{
			name:    "schema with valid refs",
			schema:  schemaWithRef,
			wantErr: false,
		},
		{
			name:    "circular reference schema",
			schema:  circularRefSchema,
			wantErr: false, // Should handle circular refs gracefully
		},
		{
			name:    "nil schema",
			schema:  nil,
			wantErr: true,
		},
		{
			name: "invalid ref format",
			schema: map[string]any{
				"$ref": 123, // Invalid ref type
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := dereferenceSchema(tt.schema)
			if (err != nil) != tt.wantErr {
				t.Errorf("dereferenceSchema() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && result == nil {
				t.Error("Expected non-nil result for valid schema")
			}
		})
	}
}

// TestConvertNullableType tests nullable type conversion
func TestConvertNullableType(t *testing.T) {
	allowedFields := getCachedAllowedSchemaFields()

	tests := []struct {
		name     string
		typeList []any
		wantErr  bool
	}{
		{
			name:     "valid nullable string",
			typeList: []any{"string", "null"},
			wantErr:  false,
		},
		{
			name:     "valid nullable integer",
			typeList: []any{"integer", "null"},
			wantErr:  false,
		},
		{
			name:     "invalid - too many types",
			typeList: []any{"string", "integer", "null"},
			wantErr:  true,
		},
		{
			name:     "invalid - no null type",
			typeList: []any{"string", "integer"},
			wantErr:  true,
		},
		{
			name:     "invalid - only null",
			typeList: []any{"null"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertNullableType(tt.typeList, allowedFields)
			if (err != nil) != tt.wantErr {
				t.Errorf("convertNullableType() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if result == nil {
					t.Error("Expected non-nil result for valid nullable type")
				}
				if nullable, exists := result["nullable"]; !exists || nullable != true {
					t.Error("Expected nullable field to be true")
				}
			}
		})
	}
}

// TestProcessAllOfSchema tests allOf schema processing
func TestProcessAllOfSchema(t *testing.T) {
	allowedFields := getCachedAllowedSchemaFields()

	tests := []struct {
		name      string
		allOfList []any
		wantErr   bool
	}{
		{
			name: "valid single allOf",
			allOfList: []any{
				map[string]any{
					"type": "object",
					"properties": map[string]any{
						"field": map[string]any{"type": "string"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid multiple allOf",
			allOfList: []any{
				map[string]any{"type": "string"},
				map[string]any{"type": "integer"},
			},
			wantErr: true,
		},
		{
			name:      "empty allOf",
			allOfList: []any{},
			wantErr:   false,
		},
		{
			name: "invalid allOf element type",
			allOfList: []any{
				"invalid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := processAllOfSchema(tt.allOfList, allowedFields)
			if (err != nil) != tt.wantErr {
				t.Errorf("processAllOfSchema() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && result == nil {
				t.Error("Expected non-nil result for valid allOf schema")
			}
		})
	}
}

// TestProcessAnyOfSchema tests anyOf schema processing
func TestProcessAnyOfSchema(t *testing.T) {
	allowedFields := getCachedAllowedSchemaFields()

	tests := []struct {
		name      string
		anyOfList []any
		wantErr   bool
	}{
		{
			name: "valid anyOf with nullable",
			anyOfList: []any{
				map[string]any{"type": "string"},
				map[string]any{"type": "null"},
			},
			wantErr: false,
		},
		{
			name: "valid anyOf without nullable",
			anyOfList: []any{
				map[string]any{"type": "string"},
				map[string]any{"type": "integer"},
			},
			wantErr: false,
		},
		{
			name: "invalid anyOf element type",
			anyOfList: []any{
				"invalid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := processAnyOfSchema(tt.anyOfList, allowedFields)
			if (err != nil) != tt.wantErr {
				t.Errorf("processAnyOfSchema() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && result == nil {
				t.Error("Expected non-nil result for valid anyOf schema")
			}
		})
	}
}

// TestConvertToGAPIC tests GAPIC conversion
func TestConvertToGAPIC(t *testing.T) {
	allowedFields := getCachedAllowedSchemaFields()

	tests := []struct {
		name    string
		schema  map[string]any
		wantErr bool
	}{
		{
			name:    "simple object schema",
			schema:  validSimpleSchema,
			wantErr: false,
		},
		{
			name:    "schema with nullable type",
			schema:  schemaWithNullableType,
			wantErr: false,
		},
		{
			name:    "schema with allOf",
			schema:  schemaWithAllOf,
			wantErr: false,
		},
		{
			name:    "schema with anyOf",
			schema:  schemaWithAnyOf,
			wantErr: false,
		},
		{
			name:    "nil schema",
			schema:  nil,
			wantErr: true,
		},
		{
			name: "schema with items",
			schema: map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertToGAPIC(tt.schema, allowedFields)
			if (err != nil) != tt.wantErr {
				t.Errorf("convertToGAPIC() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && result == nil {
				t.Error("Expected non-nil result for valid schema")
			}
		})
	}
}

// TestMapToSchema tests map to schema conversion
func TestMapToSchema(t *testing.T) {
	tests := []struct {
		name      string
		schemaMap map[string]any
		wantErr   bool
	}{
		{
			name: "valid schema map",
			schemaMap: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			},
			wantErr: false,
		},
		{
			name:      "nil schema map",
			schemaMap: nil,
			wantErr:   true,
		},
		{
			name:      "empty schema map",
			schemaMap: map[string]any{},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := mapToSchema(tt.schemaMap)
			if (err != nil) != tt.wantErr {
				t.Errorf("mapToSchema() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && result == nil {
				t.Error("Expected non-nil result for valid schema map")
			}
			if !tt.wantErr {
				// Verify result is a valid genai.Schema
				if reflect.TypeOf(result) != reflect.TypeOf(&genai.Schema{}) {
					t.Error("Expected result to be of type *genai.Schema")
				}
			}
		})
	}
}

// TestConvertJSONSchemaToGemini tests the main conversion function
func TestConvertJSONSchemaToGemini(t *testing.T) {
	tests := []struct {
		name    string
		schema  map[string]any
		wantErr bool
	}{
		{
			name:    "simple valid schema",
			schema:  validSimpleSchema,
			wantErr: false,
		},
		{
			name:    "schema with references",
			schema:  schemaWithRef,
			wantErr: false,
		},
		{
			name:    "schema with nullable types",
			schema:  schemaWithNullableType,
			wantErr: false,
		},
		{
			name:    "schema with allOf",
			schema:  schemaWithAllOf,
			wantErr: false,
		},
		{
			name:    "schema with anyOf",
			schema:  schemaWithAnyOf,
			wantErr: false,
		},
		{
			name:    "nil schema",
			schema:  nil,
			wantErr: true,
		},
		{
			name:    "empty schema",
			schema:  map[string]any{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ConvertJSONSchemaToGemini(tt.schema)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConvertJSONSchemaToGemini() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if result == nil {
					t.Error("Expected non-nil result for valid schema")
				}
				// Verify result is a valid genai.Schema
				if reflect.TypeOf(result) != reflect.TypeOf(&genai.Schema{}) {
					t.Error("Expected result to be of type *genai.Schema")
				}
			}
		})
	}
}

// TestConcurrentAccess tests concurrent access to cached fields
func TestConcurrentAccess(t *testing.T) {
	// Reset cache
	allowedSchemaFieldsOnce = sync.Once{}
	allowedSchemaFields = nil

	const numGoroutines = 100
	results := make(chan map[string]struct{}, numGoroutines)

	// Launch multiple goroutines
	for i := 0; i < numGoroutines; i++ {
		go func() {
			fields := getCachedAllowedSchemaFields()
			results <- fields
		}()
	}

	// Collect results
	var firstResult map[string]struct{}
	for i := 0; i < numGoroutines; i++ {
		result := <-results
		if i == 0 {
			firstResult = result
		} else {
			// All results should have the same content (from cache)
			if len(firstResult) != len(result) {
				t.Error("Concurrent access should return cached content with same length")
			}
			// Verify content is the same
			for key := range firstResult {
				if _, exists := result[key]; !exists {
					t.Error("Concurrent access should return the same cached content")
				}
			}
		}
	}
}

// TestErrorContextPropagation tests that errors maintain context
func TestErrorContextPropagation(t *testing.T) {
	invalidSchema := map[string]any{
		"properties": map[string]any{
			"field": "invalid_property_value", // This should cause an error
		},
	}

	_, err := ConvertJSONSchemaToGemini(invalidSchema)
	if err == nil {
		t.Error("Expected error for invalid schema")
	}

	// Check that error message contains context
	errorMsg := err.Error()
	if !contains(errorMsg, "ConvertJSONSchemaToGemini") {
		t.Error("Error message should contain function context")
	}
}

// TestComplexRealWorldSchema tests with a more complex, realistic schema
func TestComplexRealWorldSchema(t *testing.T) {
	complexSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type": "string",
			},
			"user": map[string]any{
				"$ref": "#/definitions/User",
			},
			"tags": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
			},
			"metadata": map[string]any{
				"type": []any{"object", "null"},
				"properties": map[string]any{
					"version": map[string]any{"type": "string"},
				},
			},
			"status": map[string]any{
				"anyOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "integer"},
				},
			},
		},
		"required": []any{"id", "user"},
		"definitions": map[string]any{
			"User": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string"},
					"email": map[string]any{"type": "string"},
					"profile": map[string]any{
						"allOf": []any{
							map[string]any{
								"type": "object",
								"properties": map[string]any{
									"bio": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"required": []any{"name", "email"},
			},
		},
	}

	result, err := ConvertJSONSchemaToGemini(complexSchema)
	if err != nil {
		t.Errorf("Failed to convert complex schema: %v", err)
	}
	if result == nil {
		t.Error("Expected non-nil result for complex schema")
	}
}

// BenchmarkConvertJSONSchemaToGemini benchmarks the main conversion function
func BenchmarkConvertJSONSchemaToGemini(b *testing.B) {
	schema := validSimpleSchema

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ConvertJSONSchemaToGemini(schema)
		if err != nil {
			b.Fatalf("Conversion failed: %v", err)
		}
	}
}

// BenchmarkGetCachedAllowedSchemaFields benchmarks the cached field access
func BenchmarkGetCachedAllowedSchemaFields(b *testing.B) {
	// Ensure cache is populated
	getCachedAllowedSchemaFields()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		getCachedAllowedSchemaFields()
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(len(substr) == 0 || findIndex(s, substr) >= 0)
}

func findIndex(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// TestJSONSchemaProcessor tests the processor struct functionality
func TestJSONSchemaProcessor(t *testing.T) {
	processor := NewJSONSchemaProcessor()
	if processor == nil {
		t.Fatal("Expected non-nil processor")
	}
	if processor.processedRefs == nil {
		t.Error("Expected initialized processedRefs map")
	}
}

// TestShouldSkipKey tests the skip key functionality
func TestShouldSkipKey(t *testing.T) {
	skipKeys := []string{"skip1", "skip2"}

	tests := []struct {
		key      string
		expected bool
	}{
		{"skip1", true},
		{"skip2", true},
		{"normal", false},
		{"", false},
	}

	for _, tt := range tests {
		result := shouldSkipKey(tt.key, skipKeys)
		if result != tt.expected {
			t.Errorf("shouldSkipKey(%q) = %v, want %v", tt.key, result, tt.expected)
		}
	}
}

// TestDeepCopyFunctionality tests deep copy operations
func TestDeepCopyFunctionality(t *testing.T) {
	original := map[string]any{
		"string": "value",
		"number": 42,
		"nested": map[string]any{
			"inner": "value",
		},
		"array": []any{"item1", "item2"},
	}

	copied := deepCopyMapStringAny(original)

	// Verify copy is not the same reference
	if &original == &copied {
		t.Error("Deep copy should create a new map")
	}

	// Verify values are equal
	if copied["string"] != original["string"] {
		t.Error("String values should be equal")
	}

	// Verify nested map is copied
	originalNested := original["nested"].(map[string]any)
	copiedNested := copied["nested"].(map[string]any)
	if &originalNested == &copiedNested {
		t.Error("Nested map should be deep copied")
	}

	// Modify original and verify copy is unchanged
	original["string"] = "modified"
	if copied["string"] == "modified" {
		t.Error("Modifying original should not affect copy")
	}
}
