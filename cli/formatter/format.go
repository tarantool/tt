package formatter

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/apex/log"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"gopkg.in/yaml.v2"
)

// MakeOutput returns formatted output depending on specified output format and formatting options.
func MakeOutput(data string, outputFormat Format, opts *Opts) string {
	switch outputFormat {
	case DefaultFormat, YamlFormat:
		return fmt.Sprintf("%s\n", data)
	case LuaFormat:
		return fmt.Sprintf("%s\n", getRenderedLua(data))
	case TableFormat, TTableFormat:
		return fmt.Sprintf("%s", getRenderedTables(data, opts))
	default:
		panic("Unknown render case")
	}
}

// IsTTableFormat returns true if outputFormat is TTableFormat.
func IsTTableFormat(outputFormat Format) bool {
	if outputFormat == TTableFormat {
		return true
	}

	return false
}

// decodeYamlMap decodes yaml string as map[string]renderNode content.
func decodeYamlMap(input string) (map[string]renderNode, error) {
	var decodedRes map[string]renderNode
	err := yaml.Unmarshal([]byte(input), &decodedRes)
	if err != nil {
		return nil, err
	}

	return decodedRes, nil
}

// decodeYamlArr decodes yaml string as []renderNode content.
func decodeYamlArr(input string) ([]renderNode, error) {
	var decodedRes []renderNode
	err := yaml.Unmarshal([]byte(input), &decodedRes)
	if err != nil {
		return nil, err
	}

	return decodedRes, nil
}

// arrsOnlyInBatch detects whether batch consist only arrays and
// whether there is only one array in it.
func arrsOnlyInBatch(batch []renderNode) (bool, bool, error) {
	var arrsOnly = true
	for _, node := range batch {
		if getRenderNodeType(node) != arrayRenderNodeType {
			arrsOnly = false
			break
		}
	}

	var singleArrWithArrs = false
	if len(batch) == 1 {
		singleArrWithArrs = true
		for _, node := range batch {
			nodeBytes, err := yaml.Marshal(node)
			if err != nil {
				return false, false, err
			}
			iterableNode, err := decodeYamlArr(string(nodeBytes))
			if err != nil {
				return false, false, err
			}
			for _, nodeMember := range iterableNode {
				if getRenderNodeType(nodeMember) != arrayRenderNodeType {
					singleArrWithArrs = false
				}
			}
		}
	}

	return arrsOnly, singleArrWithArrs, nil
}

// constructHeaderRowForArrs constructs header row for case with arrays.
func constructHeaderRowForArrs(batch []renderNode) table.Row {
	var headerLen = 1
	for _, node := range batch {
		nodeLen := len(node.([]interface{}))
		if nodeLen > headerLen {
			headerLen = nodeLen
		}
	}

	var header table.Row
	for i := 1; i <= headerLen; i++ {
		header = append(header, "col"+strconv.Itoa(i))
	}

	return header
}

// isRenderMapsKeysEqual checks keys equal for maps[string]renderNodes.
func isRenderMapsKeysEqual(x map[string]renderNode, y map[string]renderNode) bool {
	var keysX, keysY []string

	for k := range x {
		keysX = append(keysX, k)
	}
	for k := range y {
		keysY = append(keysY, k)
	}
	sort.Strings(keysX)
	sort.Strings(keysY)

	if len(keysX) != len(keysY) {
		return false
	}

	for k, v := range keysX {
		if keysY[k] != v {
			return false
		}
	}

	return true
}

// convertMapIntrfToMapStr converts map[interface] to map[string].
func convertMapIntrfToMapStr(v interface{}) interface{} {
	switch x := v.(type) {
	case map[interface{}]interface{}:
		m := map[string]interface{}{}
		for k, v2 := range x {
			switch k2 := k.(type) {
			case string:
				m[k2] = convertMapIntrfToMapStr(v2)
			default:
				m[fmt.Sprint(k)] = convertMapIntrfToMapStr(v2)
			}
		}
		v = m

	case []interface{}:
		for i, v2 := range x {
			x[i] = convertMapIntrfToMapStr(v2)
		}

	case map[string]interface{}:
		for k, v2 := range x {
			x[k] = convertMapIntrfToMapStr(v2)
		}
	}

	return v
}

// getRenderNodeType returns renderNode type.
func getRenderNodeType(node renderNode) int {
	switch node.(type) {
	case map[interface{}]interface{}:
		return mapRenderNodeType
	case []interface{}:
		return arrayRenderNodeType
	default:
		return scalarRenderNodeType
	}
}

// isRenderNodesTypesEqual checks renderNode equal.
func isRenderNodesTypesEqual(x renderNode, y renderNode) bool {
	return getRenderNodeType(x) == getRenderNodeType(y)
}

// constructHeaderRowForMap constructs header row for case with map.
func constructHeaderRowForMap(keys []string) table.Row {
	var headerRow table.Row
	var headerBuffer []string
	for _, headerCalVal := range keys {
		strVal := fmt.Sprintf("%v", headerCalVal)
		_, err := strconv.ParseInt(strVal, 10, 64)
		if err != nil {
			headerBuffer = append(headerBuffer, strVal)
		} else {
			headerBuffer = append(headerBuffer, "col"+strVal)
		}
	}
	for _, value := range headerBuffer {
		headerRow = append(headerRow, value)
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

	for rowNum, row := range rowsRaw {
		if len(row) < rowsRawTransposedCap {
			for i := 0; i < rowsRawTransposedCap-len(row); i++ {
				rowsRaw[rowNum] = append(rowsRaw[rowNum], "")
			}
		}
	}

	var rowsRawTransposed []table.Row

	for i := 0; i < rowsRawTransposedCap; i++ {
		var rowTransposed table.Row
		for j := 0; j < len(rowsRaw); j++ {
			rowTransposed = append(rowTransposed, rowsRaw[j][i])
		}
		rowsRawTransposed = append(rowsRawTransposed, rowTransposed)
	}

	return rowsRawTransposed
}

// renderMapsWithEqualKeys returns maps with equal keys as single table string.
func renderMapsWithEqualKeys(maps []map[string]renderNode, opts *Opts) (string, error) {
	var commonKeys []string
	for mapKey := range maps[0] {
		commonKeys = append(commonKeys, mapKey)
	}

	sort.Strings(commonKeys)

	t := table.NewWriter()
	var rows []table.Row
	rows = append(rows, constructHeaderRowForMap(commonKeys))

	for _, mapVal := range maps {
		var rowVals table.Row
		for _, commonKey := range commonKeys {
			if getRenderNodeType(mapVal[commonKey]) == scalarRenderNodeType {
				switch mapVal[commonKey].(type) {
				case float64, float32:
					rowVals = append(rowVals,
						strconv.FormatFloat(mapVal[commonKey].(float64), 'f', -1, 64))
				case nil:
					rowVals = append(rowVals, "nil")
				default:
					rowVals = append(rowVals, fmt.Sprintf("%v", mapVal[commonKey]))
				}
			} else {
				// Encoding as json value for map/array table cell cases.
				jsonVal, err := json.Marshal(convertMapIntrfToMapStr(mapVal[commonKey]))
				if err != nil {
					return "", err
				}
				rowVals = append(rowVals, string(jsonVal))
			}
		}
		rows = append(rows, rowVals)
	}

	t.Style().Options.SeparateRows = true
	var rowsAmount = len(rows)
	if opts.TransposeMode {
		rows = transposeRows(rows)
	}
	t.AppendRows(rows)

	if opts.NoGraphics {
		t.SetStyle(table.Style{Box: StyleWithoutGraphics})
	}
	if opts.ColWidthMax > 0 {
		colWidthTransformer := text.Transformer(func(val interface{}) string {
			str := fmt.Sprintf("%v", val)
			widthMax := opts.ColWidthMax
			if utf8.RuneCountInString(str) > widthMax {
				firstLine := string([]rune(str)[:widthMax])
				remainingLines := string([]rune(str)[widthMax:])
				firstLine = firstLine + "+"
				remainingLines = text.InsertEveryN(remainingLines, '+', widthMax-1)
				return firstLine + remainingLines
			}
			return fmt.Sprintf("%v", val)
		})
		var columnConfigs []table.ColumnConfig
		for i := 1; i <= len(commonKeys); i++ {
			columnConfigs = append(columnConfigs,
				table.ColumnConfig{
					Number:      i,
					Transformer: colWidthTransformer,
					WidthMax:    opts.ColWidthMax,
				},
			)
		}
		t.SetColumnConfigs(columnConfigs)
	}

	if opts.TableDialect == MarkdownTableDialect {
		markdownTable := strings.Split(t.RenderMarkdown(), "\n")
		if !opts.TransposeMode {
			markdownTable = insertStrToStrSlice(markdownTable, 0,
				constructMarkdownEmptyRow(len(commonKeys)))
			markdownTable = insertStrToStrSlice(markdownTable, 1,
				constructMarkdownHeaderSeparatorRow(len(commonKeys)))
		} else {
			markdownTable = insertStrToStrSlice(markdownTable, 0,
				constructMarkdownEmptyRow(rowsAmount))
			markdownTable = insertStrToStrSlice(markdownTable, 1,
				constructMarkdownHeaderSeparatorRow(rowsAmount))
		}
		var res string
		for _, v := range markdownTable {
			res = res + v + "\n"
		}
		return res + "\n", nil
	}
	if opts.TableDialect == JiraTableDialect {
		return t.RenderMarkdown() + "\n\n", nil
	}

	return t.Render() + "\n", nil
}

// mapsOnlyInBatch checks whether batch consist only maps.
func mapsOnlyInBatch(batch []renderNode) bool {
	var mapsOnly = true
	for _, node := range batch {
		if getRenderNodeType(node) != mapRenderNodeType {
			mapsOnly = false
			break
		}
	}

	return mapsOnly
}

// scalarsOnlyInBatch checks whether batch consist only scalars.
func scalarsOnlyInBatch(batch []renderNode) bool {
	var scalarsOnly = true
	for _, node := range batch {
		if getRenderNodeType(node) != scalarRenderNodeType {
			scalarsOnly = false
			break
		}
	}

	return scalarsOnly
}

// insertStrToStrSlice inserts string to string slice.
func insertStrToStrSlice(slice []string, index int, value string) []string {
	if len(slice) == index {
		return append(slice, value)
	}
	slice = append(slice[:index+1], slice[index:]...)
	slice[index] = value

	return slice
}

// constructMarkdownHeaderSeparatorRow constructs separator row in markdown notation.
func constructMarkdownHeaderSeparatorRow(totalRowAmount int) string {
	var result = "|-"
	for i := 1; i < totalRowAmount; i++ {
		result = result + "|-"
	}
	result = result + "|"

	return result
}

// constructMarkdownEmptyRow constructs empty row in markdown notation.
func constructMarkdownEmptyRow(totalRowAmount int) string {
	var result = "| "
	for i := 1; i < totalRowAmount; i++ {
		result = result + "| "
	}
	result = result + "|"

	return result
}

// renderSingleColumnTable returns table with single column as string.
func renderSingleColumnTable(renderBatch []renderNode, opts *Opts) string {
	t := table.NewWriter()
	var rows []table.Row
	rows = append(rows, table.Row{"col1"})
	for _, node := range renderBatch {
		switch node.(type) {
		case float64, float32:
			rows = append(rows, []interface{}{strconv.FormatFloat(node.(float64), 'f', -1, 64)})
		case nil:
			rows = append(rows, []interface{}{"nil"})
		default:
			rows = append(rows, []interface{}{fmt.Sprintf("%v", node)})
		}
	}

	var rowsAmount = len(rows)
	if opts.TransposeMode {
		rows = transposeRows(rows)
	}
	t.AppendRows(rows)
	t.Style().Options.SeparateRows = true

	if opts.NoGraphics {
		t.SetStyle(table.Style{Box: StyleWithoutGraphics})
	}
	if opts.ColWidthMax > 0 {
		colWidthTransformer := text.Transformer(func(val interface{}) string {
			str := fmt.Sprintf("%v", val)
			widthMax := opts.ColWidthMax
			if utf8.RuneCountInString(str) > widthMax {
				firstLine := string([]rune(str)[:widthMax])
				remainingLines := string([]rune(str)[widthMax:])
				firstLine = firstLine + "+"
				remainingLines = text.InsertEveryN(remainingLines, '+', widthMax-1)
				return firstLine + remainingLines
			}
			return fmt.Sprintf("%v", val)
		})
		var columnConfigs []table.ColumnConfig
		for i := 1; i <= rowsAmount; i++ {
			columnConfigs = append(columnConfigs,
				table.ColumnConfig{
					Number:      i,
					Transformer: colWidthTransformer,
					WidthMax:    opts.ColWidthMax,
				},
			)
		}
		t.SetColumnConfigs(columnConfigs)
	}

	if opts.TableDialect == MarkdownTableDialect {
		markdownTable := strings.Split(t.RenderMarkdown(), "\n")
		if !opts.TransposeMode {
			markdownTable = insertStrToStrSlice(markdownTable, 0,
				constructMarkdownEmptyRow(1))
			markdownTable = insertStrToStrSlice(markdownTable, 1,
				constructMarkdownHeaderSeparatorRow(1))
		} else {
			markdownTable = insertStrToStrSlice(markdownTable, 0,
				constructMarkdownEmptyRow(rowsAmount))
			markdownTable = insertStrToStrSlice(markdownTable, 1,
				constructMarkdownHeaderSeparatorRow(rowsAmount))
		}
		var res string
		for _, v := range markdownTable {
			res = res + v + "\n"
		}
		return res + "\n"
	}
	if opts.TableDialect == JiraTableDialect {
		return t.RenderMarkdown() + "\n\n"
	}

	return t.Render() + "\n"
}

// castRenderNodeToMap casts renderNode to map[string]renderNode.
func castRenderNodeToMap(node renderNode) (map[string]renderNode, error) {
	var castedRes map[string]renderNode
	var err error
	nodeYaml, err := yaml.Marshal(node)
	if err != nil {
		return nil, err
	}
	castedRes, err = decodeYamlMap(string(nodeYaml))
	if err != nil {
		return nil, err
	}

	return castedRes, nil
}

// renderArrs returns table as string for arrays case.
func renderArrs(renderBatch []renderNode, opts *Opts) (string, error) {
	t := table.NewWriter()

	var rows []table.Row
	rows = append(rows, constructHeaderRowForArrs(renderBatch))
	for _, node := range renderBatch {
		nodeBytes, err := yaml.Marshal(node)
		if err != nil {
			return "", err
		}
		nodeIterable, err := decodeYamlArr(string(nodeBytes))
		if err != nil {
			return "", err
		}
		var row []interface{}
		for _, rowColVal := range nodeIterable {
			if getRenderNodeType(rowColVal) == scalarRenderNodeType {
				switch rowColVal.(type) {
				case float64, float32:
					row = append(row, strconv.FormatFloat(rowColVal.(float64), 'f', -1, 64))
				case nil:
					row = append(row, "nil")
				default:
					row = append(row, fmt.Sprintf("%v", rowColVal))
				}
			} else {
				// Encoding as json value for map/array table cell cases.
				jsonVal, err := json.Marshal(convertMapIntrfToMapStr(rowColVal))
				if err != nil {
					return "", err
				}
				row = append(row, string(jsonVal))
			}
		}
		rows = append(rows, row)
	}
	t.Style().Options.SeparateRows = true
	var rowsAmount = len(rows)
	if opts.TransposeMode {
		rows = transposeRows(rows)
	}
	t.AppendRows(rows)

	if opts.NoGraphics {
		t.SetStyle(table.Style{Box: StyleWithoutGraphics})
	}
	if opts.ColWidthMax > 0 {
		colWidthTransformer := text.Transformer(func(val interface{}) string {
			str := fmt.Sprintf("%v", val)
			widthMax := opts.ColWidthMax
			if utf8.RuneCountInString(str) > widthMax {
				firstLine := string([]rune(str)[:widthMax])
				remainingLines := string([]rune(str)[widthMax:])
				firstLine = firstLine + "+"
				remainingLines = text.InsertEveryN(remainingLines, '+', widthMax-1)
				return firstLine + remainingLines
			}
			return fmt.Sprintf("%v", val)
		})
		var columnConfigs []table.ColumnConfig
		for i := 1; i <= len(constructHeaderRowForArrs(renderBatch)); i++ {
			columnConfigs = append(columnConfigs,
				table.ColumnConfig{
					Number:      i,
					Transformer: colWidthTransformer,
					WidthMax:    opts.ColWidthMax,
				},
			)
		}
		t.SetColumnConfigs(columnConfigs)
	}

	if opts.TableDialect == MarkdownTableDialect {
		markdownTable := strings.Split(t.RenderMarkdown(), "\n")
		if !opts.TransposeMode {
			markdownTable = insertStrToStrSlice(markdownTable, 0,
				constructMarkdownEmptyRow(len(constructHeaderRowForArrs(renderBatch))))
			markdownTable = insertStrToStrSlice(markdownTable, 1,
				constructMarkdownHeaderSeparatorRow(len(constructHeaderRowForArrs(renderBatch))))
		} else {
			markdownTable = insertStrToStrSlice(markdownTable, 0,
				constructMarkdownEmptyRow(rowsAmount))
			markdownTable = insertStrToStrSlice(markdownTable, 1,
				constructMarkdownHeaderSeparatorRow(rowsAmount))
		}
		var res string
		for _, v := range markdownTable {
			res = res + v + "\n"
		}
		return res + "\n", nil
	}
	if opts.TableDialect == JiraTableDialect {
		return t.RenderMarkdown() + "\n\n", nil
	}

	return t.Render() + "\n", nil
}

// parseRenderBatch parses render batch and return tables as string for it.
func parseRenderBatch(renderBatch []renderNode, opts *Opts) (string, error) {
	if scalarsOnlyInBatch(renderBatch) {
		return renderSingleColumnTable(renderBatch, opts), nil
	}

	if mapsOnlyInBatch(renderBatch) {
		var renderNodeMaps []map[string]renderNode
		for _, node := range renderBatch {
			castedNode, err := castRenderNodeToMap(node)
			if err != nil {
				return "", err
			}
			renderNodeMaps = append(renderNodeMaps, castedNode)
		}

		var renderMapsBatchs = make([][]map[string]renderNode, len(renderNodeMaps))
		var batchPointer = 0
		renderMapsBatchs[batchPointer] = append(renderMapsBatchs[batchPointer], renderNodeMaps[0])

		for i := 0; i < len(renderNodeMaps)-1; i++ {
			if isRenderMapsKeysEqual(renderNodeMaps[i], renderNodeMaps[i+1]) {
				renderMapsBatchs[batchPointer] = append(
					renderMapsBatchs[batchPointer],
					renderNodeMaps[i+1])
			} else {
				batchPointer = batchPointer + 1
				renderMapsBatchs[batchPointer] = append(
					renderMapsBatchs[batchPointer],
					renderNodeMaps[i+1])
			}
		}

		var res, batchRes string
		var err error
		for _, batch := range renderMapsBatchs {
			if len(batch) != 0 {
				batchRes, err = renderMapsWithEqualKeys(batch, opts)
				if err != nil {
					return "", err
				}
				if opts.NoGraphics {
					batchRes = batchRes + "\n"
				}
				res = res + batchRes
			}
		}

		return res, nil
	}

	onlyArrs, singleArrWithArrs, err := arrsOnlyInBatch(renderBatch)
	if err != nil {
		return "", err
	}

	if onlyArrs {
		if !singleArrWithArrs {
			result, err := renderArrs(renderBatch, opts)
			if err != nil {
				return "", err
			}
			return result, nil
		} else {
			// Unpack array with array(s).
			var result string
			for _, node := range renderBatch {
				bytesNode, err := json.Marshal(convertMapIntrfToMapStr(node))
				if err != nil {
					return "", err
				}
				iterableNode, err := decodeYamlArr(string(bytesNode))
				if err != nil {
					return "", err
				}
				barchRes, err := renderArrs(iterableNode, opts)
				if err != nil {
					return "", err
				}
				result = result + barchRes
			}
			return result, nil
		}
	}

	return "", fmt.Errorf("unknown parsing case with current render batch")
}

// getRenderedTables returns tables as string for table/ttable output formats.
func getRenderedTables(inputYaml string, opts *Opts) string {
	// Handle empty input from remote console.
	if inputYaml == "---\n- \n...\n" || inputYaml == "---\n-\n...\n" {
		inputYaml = "--- ['']\n...\n"
	}
	if renderNodes, err := decodeYamlArr(inputYaml); err != nil {
		log.Errorf("unexpected server response: not yaml array, cannot render tables: %v", err)
		return inputYaml
	} else {
		var renderBatchs = make([][]renderNode, len(renderNodes))
		var batchPointer = 0
		renderBatchs[batchPointer] = append(renderBatchs[batchPointer], renderNodes[0])

		for i := 0; i < len(renderNodes)-1; i++ {
			if isRenderNodesTypesEqual(renderNodes[i], renderNodes[i+1]) {
				renderBatchs[batchPointer] = append(renderBatchs[batchPointer], renderNodes[i+1])
			} else {
				batchPointer = batchPointer + 1
				renderBatchs[batchPointer] = append(renderBatchs[batchPointer], renderNodes[i+1])
			}
		}

		var res, batchRes string
		var err error
		for _, batch := range renderBatchs {
			if len(batch) != 0 {
				batchRes, err = parseRenderBatch(batch, opts)
				if err != nil {
					log.Errorf("unexpected internal parser result, cannot render tables: %v", err)
					return inputYaml
				}
				res = res + batchRes
				if opts.NoGraphics {
					res = res + "\n"
				}
			}
		}

		return res
	}
}

// luaEncodeElement encodes element to lua string.
func luaEncodeElement(elem interface{}) string {
	switch elem.(type) {
	case map[interface{}]interface{}:
		res := "{"
		for k, v := range elem.(map[interface{}]interface{}) {
			if v != nil {
				if reflect.TypeOf(v).String() == "string" {
					v = "\"" + fmt.Sprintf("%v", v) + "\""
				}
			} else {
				v = "nil"
			}

			if reflect.TypeOf(k).String() == "string" {
				res = res + fmt.Sprintf("%v", k) + " = " + luaEncodeElement(v) + ","
			} else {
				res = res + "[" + fmt.Sprintf("%v", k) + "] = " + luaEncodeElement(v) + ","
			}
		}
		return res + "}"
	case []interface{}:
		res := "{"
		for k, v := range elem.([]interface{}) {
			if v != nil {
				if reflect.TypeOf(v).String() == "string" {
					v = "\"" + fmt.Sprintf("%v", v) + "\""
				}
			} else {
				v = "nil"
			}
			if k < len(elem.([]interface{}))-1 {
				res = res + luaEncodeElement(v) + ", "
			} else {
				res = res + luaEncodeElement(v)
			}
		}
		return res + "}"
	default:
		if elem == nil {
			return "nil"
		}
		return fmt.Sprintf("%v", elem)
	}
}

// getRenderedLua returns lua string by yaml string input.
func getRenderedLua(inputYaml string) string {
	// Handle empty input from remote console.
	if inputYaml == "---\n...\n" {
		inputYaml = "--- ['']\n...\n"
	}
	var decodedYaml interface{}
	if err := yaml.Unmarshal([]byte(inputYaml), &decodedYaml); err == nil {
		var res string
		for i, unpackedVal := range decodedYaml.([]interface{}) {
			if i < len(decodedYaml.([]interface{}))-1 {
				res = res + luaEncodeElement(unpackedVal) + ", "
			} else {
				res = res + luaEncodeElement(unpackedVal)
			}
		}
		res = res + ";"
		return res
	} else {
		log.Errorf("unexpected server response: cannot render lua: %v", err)
		return inputYaml
	}
}
