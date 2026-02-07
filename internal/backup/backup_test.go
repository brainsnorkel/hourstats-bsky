package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func testItems() []map[string]types.AttributeValue {
	return []map[string]types.AttributeValue{
		{
			"PK":   &types.AttributeValueMemberS{Value: "RUN#2026-02-07"},
			"SK":   &types.AttributeValueMemberS{Value: "orchestrator"},
			"Data": &types.AttributeValueMemberS{Value: "hello world"},
		},
		{
			"PK":    &types.AttributeValueMemberS{Value: "RUN#2026-02-07"},
			"SK":    &types.AttributeValueMemberS{Value: "fetcher"},
			"Count": &types.AttributeValueMemberN{Value: "42"},
		},
		{
			"PK":      &types.AttributeValueMemberS{Value: "DAILY#2026-02-07"},
			"SK":      &types.AttributeValueMemberS{Value: "summary"},
			"IsValid": &types.AttributeValueMemberBOOL{Value: true},
		},
	}
}

func TestWriteItemsJSONL(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test-table.jsonl")
	items := testItems()

	size, err := writeItemsJSONL(filePath, items)
	if err != nil {
		t.Fatalf("writeItemsJSONL failed: %v", err)
	}

	if size <= 0 {
		t.Errorf("file size = %d, want > 0", size)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != len(items) {
		t.Fatalf("line count = %d, want %d", len(lines), len(items))
	}

	for i, line := range lines {
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i, err)
		}
	}
}

func TestWriteItemsJSONL_Empty(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "empty.jsonl")

	size, err := writeItemsJSONL(filePath, nil)
	if err != nil {
		t.Fatalf("writeItemsJSONL with empty items failed: %v", err)
	}

	if size != 0 {
		t.Errorf("file size = %d, want 0 for empty items", size)
	}
}

func TestWriteItemsJSONLCompressed(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test-table.jsonl.gz")
	items := testItems()

	size, err := writeItemsJSONLCompressed(filePath, items)
	if err != nil {
		t.Fatalf("writeItemsJSONLCompressed failed: %v", err)
	}

	if size <= 0 {
		t.Errorf("compressed file size = %d, want > 0", size)
	}

	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("failed to stat compressed file: %v", err)
	}
	if info.Size() == 0 {
		t.Error("compressed file is empty")
	}
}

func TestWriteItemsJSONL_ContentStructure(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "structure.jsonl")
	items := testItems()

	if _, err := writeItemsJSONL(filePath, items); err != nil {
		t.Fatalf("writeItemsJSONL failed: %v", err)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	// Each line should contain the DynamoDB attribute value format
	// e.g. {"PK":{"Value":"RUN#2026-02-07"},...}
	if !strings.Contains(lines[0], "RUN#2026-02-07") {
		t.Errorf("line 0 missing expected PK value, got: %s", lines[0])
	}
	if !strings.Contains(lines[0], "hello world") {
		t.Errorf("line 0 missing expected Data value, got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "42") {
		t.Errorf("line 1 missing expected Count value, got: %s", lines[1])
	}
}

func TestWriteAndReadJSONL_LineCount(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "linecount.jsonl")

	items := testItems()
	if _, err := writeItemsJSONL(filePath, items); err != nil {
		t.Fatalf("writeItemsJSONL failed: %v", err)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != len(items) {
		t.Errorf("wrote %d items but file has %d lines", len(items), len(lines))
	}
}

func TestCompressedSmallerThanUncompressed(t *testing.T) {
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "plain.jsonl")
	compPath := filepath.Join(dir, "compressed.jsonl.gz")

	// Use many items to make compression meaningful
	var manyItems []map[string]types.AttributeValue
	for i := 0; i < 100; i++ {
		manyItems = append(manyItems, map[string]types.AttributeValue{
			"PK":   &types.AttributeValueMemberS{Value: "RUN#2026-02-07"},
			"SK":   &types.AttributeValueMemberS{Value: "item"},
			"Data": &types.AttributeValueMemberS{Value: "repeated data for compression test"},
		})
	}

	plainSize, err := writeItemsJSONL(plainPath, manyItems)
	if err != nil {
		t.Fatalf("writeItemsJSONL failed: %v", err)
	}

	compSize, err := writeItemsJSONLCompressed(compPath, manyItems)
	if err != nil {
		t.Fatalf("writeItemsJSONLCompressed failed: %v", err)
	}

	if compSize >= plainSize {
		t.Errorf("compressed size (%d) should be smaller than plain size (%d)", compSize, plainSize)
	}
}

func TestWriteTableMetadata(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "metadata.json")

	if err := writeTableMetadata(filePath, nil); err != nil {
		t.Fatalf("writeTableMetadata with nil should not error: %v", err)
	}

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("writeTableMetadata with nil should not create a file")
	}
}

func TestReadItemsJSONL_NotFound(t *testing.T) {
	_, err := readItemsJSONL("/nonexistent/file.jsonl", "file.jsonl")
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}
