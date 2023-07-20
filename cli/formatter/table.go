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
	"gopkg.in/yaml.v2"
)

// decodeYamlArr decodes yaml string as []any content.
func decodeYamlArr(input string) ([]any, error) {
	var decoded []any
	err := yaml.Unmarshal([]byte(input), &decoded)
	if err != nil {
		return nil, err
	}

	return decoded, nil
}

// castAnyMapToStringMap casts map[any]any to map[string]any.
func castAnyMapToStringMap(src map[any]any) map[string]any {
	dst := make(map[string]any)

	for k, v := range src {
		if strKey, ok := k.(string); ok {
			dst[strKey] = v
		} else {
			dst[fmt.Sprint(k)] = v
		}
	}

	return dst
}

// deepCastAnyMapToStringMap casts all map[any]any to map[string]any deeply.
func deepCastAnyMapToStringMap(v any) interface{} {
	switch x := v.(type) {
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
	case []any:
		for i, v2 := range x {
			x[i] = deepCastAnyMapToStringMap(v2)
		}

	case map[string]any:
		for k, v2 := range x {
			x[k] = deepCastAnyMapToStringMap(v2)
		}
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

	var mapped []map[string]any
	for _, item := range batch {
		item := item.([]any)
		itemMap := make(map[string]any)

		for i := 0; i < maxLen; i++ {
			if i < len(item) {
				itemMap[strconv.Itoa(i+1)] = item[i]
			} else {
				itemMap[strconv.Itoa(i+1)] = ""
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
func createHeader(keys []string) table.Row {
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
func renderEqualMaps(maps []map[string]any, transpose bool, opts Opts) (string, error) {
	t := newTableWriter(opts)

	var commonKeys []string
	for mapKey := range maps[0] {
		commonKeys = append(commonKeys, mapKey)
	}
	sort.Strings(commonKeys)

	var rows []table.Row
	rows = append(rows, createHeader(commonKeys))

	for _, mapVal := range maps {
		var rowVals table.Row
		for _, key := range commonKeys {
			if cellValue, err := encodeCell(mapVal[key]); err != nil {
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

// isMapKeysEqual checks keys equal for maps[string]any.
func isMapKeysEqual(x map[string]any, y map[string]any) bool {
	var keysX, keysY []string

	for k := range x {
		keysX = append(keysX, k)
	}
	for k := range y {
		keysY = append(keysY, k)
	}

	if len(keysX) != len(keysY) {
		return false
	}

	sort.Strings(keysX)
	sort.Strings(keysY)
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
		var anyMaps []map[string]any
		for _, node := range batch {
			castedMap := castAnyMapToStringMap(node.(map[any]any))
			anyMaps = append(anyMaps, castedMap)
		}

		var mapsBatchs = make([][]map[string]any, len(anyMaps))
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
	for _, row := range meta.Rows {
		index := 1
		mapped := make(map[any]any)
		for i, column := range row {
			if len(meta.Metadata) > i && meta.Metadata[i].Name != "" {
				mapped[meta.Metadata[i].Name] = column
			} else {
				mapped[index] = column
				index++
			}
		}
		nodes = append(nodes, mapped)
	}
	return nodes
}

// makeTableOutput returns tables as string for table/ttable output formats.
func makeTableOutput(input string, transpose bool, opts Opts) (string, error) {
	// Handle empty input from remote console.
	if input == "---\n- \n...\n" || input == "---\n-\n...\n" {
		input = "--- ['']\n...\n"
	}

	var meta []metadataRows
	var nodes []any

	// First of all try to read it as tuples with metadata (SQL output format).
	err := yaml.Unmarshal([]byte(input), &meta)
	if err == nil && len(meta) > 0 && len(meta[0].Rows) > 0 && len(meta[0].Metadata) > 0 {
		nodes = remapMetadataRows(meta[0])
	} else {
		// Failed. Try to read it as an array.
		nodes, err = decodeYamlArr(input)
		if err != nil {
			return "", fmt.Errorf("not yaml array, cannot render tables: %s", err)
		}
	}

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
