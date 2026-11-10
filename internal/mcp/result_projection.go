package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

func projectExecution(e *execution, tool string, mode ResultMode) *sdk.CallToolResult {
	isError := e.isError(tool) || e.cancelled
	value, err := canonicalResult(e)
	if err != nil {
		value = encodingFailureJSON
		isError = true
	}
	var text string
	if mode != ResultModeStructured {
		text, err = encodeResultText(value, isError)
	} else if isError {
		text, err = encodeResultDiagnostic(value)
	}
	if err != nil {
		value = encodingFailureJSON
		isError = true
		if mode == ResultModeStructured {
			text, _ = encodeResultDiagnostic(value)
		} else {
			text, _ = encodeResultText(value, true)
		}
	}
	result := &sdk.CallToolResult{Content: []sdk.Content{}, IsError: isError}
	if text != "" {
		result.Content = append(result.Content, &sdk.TextContent{Text: text})
	}
	if e.image != nil && len(e.image.Data) > 0 && e.image.MediaType != "" {
		result.Content = append(result.Content, &sdk.ImageContent{Data: e.image.Data, MIMEType: e.image.MediaType})
	}
	if mode != ResultModeText {
		result.StructuredContent = value
	}
	return result
}
