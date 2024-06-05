package formatter

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v2"
)

// lazyDecodeYaml decodes yaml string as []lazyMessage content.
// Each array member needs to be decoded later.
func lazyDecodeYaml(input string) ([]lazyMessage, error) {
	var decoded []lazyMessage
	err := yaml.Unmarshal([]byte(input), &decoded)
	if err != nil {
		return nil, err
	}

	return decoded, nil
}

// castMapToUMap casts map[any]any to unorderedMap[any].
// The result will be sorted by keys.
func castMapToUMap(src map[any]any) unorderedMap[any] {
	convertedSrc := make(map[any]any)
	sortedKeys := make([]any, 0, len(src))

	for k, v := range src {
		if strKey, ok := k.(string); ok {
			convertedSrc[strKey] = v
			sortedKeys = append(sortedKeys, strKey)
		} else {
			convertedSrc[fmt.Sprint(k)] = v
			sortedKeys = append(sortedKeys, fmt.Sprint(k))
		}
	}
	sort.Slice(sortedKeys, func(i, j int) bool {
		return fmt.Sprint(sortedKeys[i]) < fmt.Sprint(sortedKeys[j])
	})

	dst := createUnorderedMap[any](len(convertedSrc))
	for _, k := range sortedKeys {
		dst.insert(k, convertedSrc[k])
	}

	return dst
}

// deepCastAnyMapToStringMap casts all map[any]any to map[string]any deeply.
func deepCastAnyMapToStringMap(v any) interface{} {
	switch x := v.(type) {
	case []any:
		for i, v2 := range x {
			x[i] = deepCastAnyMapToStringMap(v2)
		}

	case map[any]any:
		m := map[string]any{}
		for k, v2 := range x {
			switch k2 := k.(type) {
			case string:
				m[k2] = deepCastAnyMapToStringMap(v2)
			default:
				m[fmt.Sprint(k)] = deepCastAnyMapToStringMap(v2)
			}
		}
		v = m
	}

	return v
}

// encodeScalar encodes a scalar into a string.
func encodeScalar(val any) string {
	switch val.(type) {
	case float64, float32:
		return strconv.FormatFloat(val.(float64), 'f', -1, 64)
	case nil:
		return "nil"
	default:
		return fmt.Sprintf("%v", val)
	}
}

// encodeJson encodes a value into json.
func encodeJson(val any) (string, error) {
	jsonData, err := json.Marshal(deepCastAnyMapToStringMap(val))
	if err != nil {
		return "", err
	}

	return string(jsonData), nil
}

// encodeCell encodes a cell value into a string.
func encodeCell(val any) (string, error) {
	if getNodeType(val) == scalarNodeType {
		return encodeScalar(val), nil
	}
	return encodeJson(val)
}

// isSingleType returns true if all elements of the batch belongs to
// the passed type.
func isSingleType(batch []any, t nodeType) bool {
	for _, node := range batch {
		if getNodeType(node) != t {
			return false
		}
	}

	return true
}

// renderScalars returns a table as string for scalars.
func renderScalars(batch []any, transpose bool, opts Opts) (string, error) {
	var arrays []any
	for _, item := range batch {
		arrays = append(arrays, []any{item})
	}
	return renderArrays(arrays, transpose, opts)
}

// isSingleArrayOfArrays returns true if the batch is a single array of arrays
// and could be represented as a single table.
func isSingleArrayOfArrays(batch []any) bool {
	if len(batch) != 1 {
		return false
	}

	if array, ok := batch[0].([]any); ok {
		return isSingleType(array, arrayNodeType)
	} else {
		return false
	}
}

// renderArrays returns a string representation of arrays.
func renderArrays(batch []any, transpose bool, opts Opts) (string, error) {
	if isSingleArrayOfArrays(batch) {
		array := batch[0].([]any)
		return renderArraysAsTable(array, transpose, opts)
	} else {
		return renderArraysAsTable(batch, transpose, opts)
	}
}

// renderArraysAsTable returns a single table as string for the set of arrays.
func renderArraysAsTable(batch []any, transpose bool, opts Opts) (string, error) {
	maxLen := 0
	for _, item := range batch {
		itemLen := len(item.([]any))
		if itemLen > maxLen {
			maxLen = itemLen
		}
	}

	var mapped []unorderedMap[any]
	for _, item := range batch {
		item := item.([]any)
		itemMap := createUnorderedMap[any](maxLen)

		for i := 0; i < maxLen; i++ {
			if i < len(item) {
				itemMap.insert(strconv.Itoa(i+1), item[i])
			} else {
				itemMap.insert(strconv.Itoa(i+1), "")
			}
		}

		mapped = append(mapped, itemMap)
	}

	return renderEqualMaps(mapped, transpose, opts)
}

// newTableWriter creates and configures new table writer.
func newTableWriter(opts Opts) table.Writer {
	t := table.NewWriter()
	t.Style().Options.SeparateRows = true
	if !opts.Graphics {
		t.SetStyle(table.Style{Box: StyleWithoutGraphics})
	}

	return t
}

// handleColumnWidth handles width max value for tables columns.
func handleColumnWidth(t table.Writer, columns int, opts Opts) {
	colWidthTransformer := text.Transformer(func(val interface{}) string {
		str := fmt.Sprintf("%v", val)
		widthMax := opts.ColumnWidthMax
		if utf8.RuneCountInString(str) > widthMax {
			first := string([]rune(str)[:widthMax])
			remaining := string([]rune(str)[widthMax:])
			return first + "+" + text.InsertEveryN(remaining, '+', widthMax-1)
		}
		return fmt.Sprintf("%v", val)
	})

	var configs []table.ColumnConfig
	for i := 1; i <= columns; i++ {
		configs = append(configs,
			table.ColumnConfig{
				Number:      i,
				Transformer: colWidthTransformer,
				WidthMax:    opts.ColumnWidthMax,
			},
		)
	}
	t.SetColumnConfigs(configs)
}

// createHeader creates a header row.
func createHeader(keys []any) table.Row {
	var headerRow table.Row
	for _, headerCalVal := range keys {
		strVal := fmt.Sprintf("%v", headerCalVal)
		_, err := strconv.ParseInt(strVal, 10, 64)
		if err != nil {
			headerRow = append(headerRow, strVal)
		} else {
			headerRow = append(headerRow, "col"+strVal)
		}
	}
	return headerRow
}

// transposeRows performs table rows transpose.
func transposeRows(rowsRaw []table.Row) []table.Row {
	var rowsRawTransposedCap = 0
	for _, row := range rowsRaw {
		if len(row) > rowsRawTransposedCap {
			rowsRawTransposedCap = len(row)
		}
	}

	var rowsRawTransposed []table.Row
	for i := 0; i < rowsRawTransposedCap; i++ {
		var rowTransposed table.Row
		for j := 0; j < len(rowsRaw); j++ {
			if i < len(rowsRaw[j]) {
				rowTransposed = append(rowTransposed, rowsRaw[j][i])
			} else {
				rowTransposed = append(rowTransposed, "")
			}
		}
		rowsRawTransposed = append(rowsRawTransposed, rowTransposed)
	}
	return rowsRawTransposed
}

// createMarkdownTable creates a table in markdown notation.
func createMarkdownTable(table []string, columns int, opts Opts) string {
	empty := "| "
	separator := "|-"
	for i := 1; i < columns; i++ {
		empty += "| "
		separator += "|-"
	}
	empty += "|"
	separator += "|"

	var result string
	for _, rows := range [][]string{[]string{empty, separator}, table} {
		for _, row := range rows {
			result += row + "\n"
		}
	}

	return result
}

// renderEqualMaps returns maps with equal keys as single table string.
func renderEqualMaps(maps []unorderedMap[any], transpose bool, opts Opts) (string, error) {
	t := newTableWriter(opts)

	var commonKeys []any
	maps[0].forEach(func(mapKey, _ any) {
		commonKeys = append(commonKeys, mapKey)
	})

	var rows []table.Row
	rows = append(rows, createHeader(commonKeys))

	for _, mapVal := range maps {
		var rowVals table.Row
		for _, key := range commonKeys {
			if cellValue, err := encodeCell(mapVal.innerMap[key]); err != nil {
				return "", err
			} else {
				rowVals = append(rowVals, cellValue)
			}
		}
		rows = append(rows, rowVals)
	}

	columnsAmount := len(commonKeys)
	rowsAmount := len(rows)
	if transpose {
		rows = transposeRows(rows)
		columnsAmount = rowsAmount
	}
	t.AppendRows(rows)

	if opts.ColumnWidthMax > 0 {
		handleColumnWidth(t, columnsAmount, opts)
	}

	if opts.TableDialect == MarkdownTableDialect {
		markdown := strings.Split(t.RenderMarkdown(), "\n")
		return createMarkdownTable(markdown, columnsAmount, opts) + "\n", nil
	}
	if opts.TableDialect == JiraTableDialect {
		return t.RenderMarkdown() + "\n\n", nil
	}

	return t.Render() + "\n", nil
}

// isMapKeysEqual checks keys equal for two unorderedMap[any].
func isMapKeysEqual(x unorderedMap[any], y unorderedMap[any]) bool {
	var keysX, keysY []any

	x.forEach(func(k, _ any) {
		keysX = append(keysX, k)
	})
	y.forEach(func(k, _ any) {
		keysY = append(keysY, k)
	})

	if len(keysX) != len(keysY) {
		return false
	}

	sort.Slice(keysX, func(i, j int) bool {
		return fmt.Sprint(keysX[i]) < fmt.Sprint(keysX[j])
	})
	sort.Slice(keysY, func(i, j int) bool {
		return fmt.Sprint(keysY[i]) < fmt.Sprint(keysY[j])
	})

	for k, v := range keysX {
		if keysY[k] != v {
			return false
		}
	}

	return true
}

// renderBatch parses renders batch and return tables as string for it.
func renderBatch(batch []any, transpose bool, opts Opts) (string, error) {
	if isSingleType(batch, scalarNodeType) {
		return renderScalars(batch, transpose, opts)
	} else if isSingleType(batch, mapNodeType) {
		var anyMaps []unorderedMap[any]
		for _, node := range batch {
			var castedMap unorderedMap[any]
			switch n := node.(type) {
			case unorderedMap[any]:
				castedMap = n
			default:
				castedMap = castMapToUMap(node.(map[any]any))
			}
			anyMaps = append(anyMaps, castedMap)
		}

		var mapsBatchs = make([][]unorderedMap[any], len(anyMaps))
		var batchPointer = 0
		mapsBatchs[batchPointer] = append(mapsBatchs[batchPointer], anyMaps[0])

		for i := 0; i < len(anyMaps)-1; i++ {
			if !isMapKeysEqual(anyMaps[i], anyMaps[i+1]) {
				batchPointer++
			}
			mapsBatchs[batchPointer] = append(mapsBatchs[batchPointer], anyMaps[i+1])
		}

		var res, batchRes string
		var err error
		for _, batch := range mapsBatchs {
			if len(batch) != 0 {
				batchRes, err = renderEqualMaps(batch, transpose, opts)
				if err != nil {
					return "", err
				}
				if !opts.Graphics {
					batchRes += "\n"
				}
				res += batchRes
			}
		}

		return res, nil
	} else if isSingleType(batch, arrayNodeType) {
		return renderArrays(batch, transpose, opts)
	} else {
		return "", fmt.Errorf("unknown parsing case with current render batch")
	}
}

// renderBatches combines multiple batches into one string.
func renderBatches(batches [][]any, transpose bool, opts Opts) (string, error) {
	var result string
	for _, batch := range batches {
		if len(batch) != 0 {
			batchStr, err := renderBatch(batch, transpose, opts)
			if err != nil {
				return "", fmt.Errorf("cannot render tables: %w", err)
			}
			result += batchStr
			if !opts.Graphics {
				result += "\n"
			}
		}
	}

	return result, nil

}

type metadataField struct {
	Name string `yaml:"name"`
}

type metadataRows struct {
	Metadata []metadataField `yaml:"metadata"`
	Rows     [][]any         `yaml:"rows"`
}

// remapMetadataRows creates maps from rows with a metainformation.
func remapMetadataRows(meta metadataRows) []any {
	var nodes []any
	maxLen := 0
	for _, row := range meta.Rows {
		if len(row) > maxLen {
			maxLen = len(row)
		}
	}
	for _, row := range meta.Rows {
		index := 1
		mapped := createUnorderedMap[any](len(row))
		for i := 0; i < maxLen; i++ {
			if i >= len(row) {
				mapped.insert(index, "")
				index++
				continue
			}

			column := row[i]
			if len(meta.Metadata) > i && meta.Metadata[i].Name != "" {
				mapped.insert(meta.Metadata[i].Name, column)
			} else {
				mapped.insert(index, column)
				index++
			}
		}
		nodes = append(nodes, mapped)
	}
	return nodes
}

func insertCollectedFields(fields metadataRows, nodes []any) []any {
	if len(fields.Metadata) > 0 && len(fields.Rows) > 0 {
		return append(nodes, remapMetadataRows(fields)...)
	}
	return nodes
}

// makeTableOutput returns tables as string for table/ttable output formats.
func makeTableOutput(input string, transpose bool, opts Opts) (string, error) {
	// Handle empty input from remote console.
	if input == "---\n- \n...\n" || input == "---\n-\n...\n" {
		input = "--- ['']\n...\n"
	}

	var nodes []any

	// We need to decode input lazy here. This is the case because we can get
	// the input as an array, where some elements have metadata.
	// So we need to decode input as an array first and then each element
	// separately: as a metadataRows type or some other any value.
	// If we decode everything as []any first, it would be problematic to
	// convert any value to metadataRows type.
	lazyNodes, err := lazyDecodeYaml(input)
	if err != nil {
		return "", fmt.Errorf("not yaml array, cannot render tables: %s", err)
	}

	var metaFields metadataRows

	for _, lazyNode := range lazyNodes {
		var meta metadataRows

		// First of all, try to read it as tuples with metadata
		// (SQL output format).
		err = lazyNode.Unmarshal(&meta)
		if err == nil && len(meta.Rows) > 0 && len(meta.Metadata) > 0 {
			if slices.Equal(meta.Metadata, metaFields.Metadata) {
				metaFields.Rows = append(metaFields.Rows, meta.Rows...)
			} else {
				nodes = insertCollectedFields(metaFields, nodes)
				metaFields = meta
			}
		} else {
			nodes = insertCollectedFields(metaFields, nodes)
			metaFields = metadataRows{}

			// Failed. Try to read it as an any.
			var node any
			err = lazyNode.Unmarshal(&node)
			if err != nil {
				return "", fmt.Errorf("not yaml any: %s", err)
			}
			nodes = append(nodes, node)
		}
	}
	nodes = insertCollectedFields(metaFields, nodes)

	if len(nodes) == 0 {
		nodes = append(nodes, []any{""})
	}

	// The code tries to combine multiple values into a one batch by type.
	var batches = make([][]any, len(nodes))
	var batchPointer = 0
	batches[batchPointer] = append(batches[batchPointer], nodes[0])
	for i := 0; i < len(nodes)-1; i++ {
		if !isNodeTypeEqual(nodes[i], nodes[i+1]) {
			batchPointer++
		}
		batches[batchPointer] = append(batches[batchPointer], nodes[i+1])
	}

	return renderBatches(batches, transpose, opts)
}
