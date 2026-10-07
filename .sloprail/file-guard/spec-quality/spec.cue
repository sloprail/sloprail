// spec/<domain>/entities/<Entity>.yaml — one entity (see .claude/skills/document-entity).
#Entity: {
	doc!:    string & =~"\\S"
	emoji?:  string
	fields!: [...#Field]
}

#Field: {
	name!: string & =~"^[a-z][A-Za-z0-9_]*$"
	// TypeSpec-like: a scalar, Record<...>, or an entity (cross-domain domain.Entity),
	// optionally a list (T[]) and/or optional (T?).
	type!:            string & =~"^(string|int32|int64|float64|bool|duration|unknown|Record<[^>]+>|([a-z0-9-]+\\.)?[A-Z][A-Za-z0-9]*)(\\[\\])?\\??$"
	doc?:             string & =~"\\S"
	constraint_expr?: string & =~"\\S"
	enum?: [string, ...string]
	if enum != _|_ {type: "string" | "string[]"}
}

// spec/<domain>/invariants/<id>.yaml — one invariant (see .claude/skills/document-invariant).
#Invariant: {
	// anchored on at least one typed mention
	predicate!: string & =~"\\{@(ent|fld):[a-z0-9-]+:[A-Z]"
	why!:       string & =~"\\S"
	// harness-mocks spec/capabilities ids
	needs?: [string & =~"^[a-z0-9]+(-[a-z0-9]+)*$", ...string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"]
}
