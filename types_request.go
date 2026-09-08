package pgserverless

// SQLRequest is the JSON request body for the /sql endpoint.
// Supports single query mode (Query field) or batch mode (Queries field).
type SQLRequest struct {
	Query     *string     `json:"query,omitempty"`
	Params    []any       `json:"params,omitempty"`
	Queries   []QueryItem `json:"queries,omitempty"`
	ArrayMode *bool       `json:"arrayMode,omitempty"`
}

// QueryItem is a single query within a batch request.
type QueryItem struct {
	Query     string `json:"query"`
	Params    []any  `json:"params,omitempty"`
	ArrayMode *bool  `json:"arrayMode,omitempty"`
}

// QueryResult is the JSON response for a single query execution.
type QueryResult struct {
	Fields     []FieldDefinition `json:"fields"`
	Command    string            `json:"command"`
	RowCount   *int              `json:"rowCount"`
	Rows       []any             `json:"rows"`
	RowAsArray bool              `json:"rowAsArray"`
}

// FieldDefinition describes a column in a query result.
type FieldDefinition struct {
	Name             string `json:"name"`
	TableID          uint32 `json:"tableID"`
	ColumnID         uint16 `json:"columnID"`
	DataTypeID       uint32 `json:"dataTypeID"`
	DataTypeSize     int16  `json:"dataTypeSize"`
	DataTypeModifier int32  `json:"dataTypeModifier"`
	Format           string `json:"format"`
}

// BatchResponse wraps multiple query results for batch mode.
type BatchResponse struct {
	Results []QueryResult `json:"results"`
}

// ErrorResponse is the JSON error response matching Neon's format.
type ErrorResponse struct {
	Message          string  `json:"message"`
	Code             *string `json:"code,omitempty"`
	Severity         *string `json:"severity,omitempty"`
	Detail           *string `json:"detail,omitempty"`
	Hint             *string `json:"hint,omitempty"`
	Position         *string `json:"position,omitempty"`
	InternalPosition *string `json:"internalPosition,omitempty"`
	InternalQuery    *string `json:"internalQuery,omitempty"`
	Where            *string `json:"where,omitempty"`
	// These five carry the client's spellings, not PostgreSQL's field names.
	// The client copies `schema`, `table`, `column`, `dataType` and
	// `constraint` onto its error object; a server sending `schemaName` and
	// the rest is sending fields it silently drops, so every one of them
	// arrived as undefined. See SPEC.md Appendix A.1.
	SchemaName     *string `json:"schema,omitempty"`
	TableName      *string `json:"table,omitempty"`
	ColumnName     *string `json:"column,omitempty"`
	DataTypeName   *string `json:"dataType,omitempty"`
	ConstraintName *string `json:"constraint,omitempty"`
	File           *string `json:"file,omitempty"`
	Line           *string `json:"line,omitempty"`
	Routine        *string `json:"routine,omitempty"`
}

// ConnInfo holds parsed connection string fields.
type ConnInfo struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
}
