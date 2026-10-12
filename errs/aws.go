package errs

import "net/http"

// AWSEntry is the HTTP status and error code string a surface returns for an error.
type AWSEntry struct {
	HTTP int
	Code string
}

// AWSTable maps (Code, Kind) to an AWSEntry for one surface. Policy lives in the service that
// owns the surface (console-api); errs only defines the shape and the lookup rule.
// ByKind overrides Default for one Kind.
type AWSTable struct {
	Default map[Code]AWSEntry
	ByKind  map[Code]map[Kind]AWSEntry
}

// InternalFailure is what a combination missing from the table returns.
var InternalFailure = AWSEntry{HTTP: http.StatusInternalServerError, Code: "InternalFailure"}

// Lookup returns the entry for a Code and Kind. found is false when the table has neither a
// Kind override nor a default: the caller gets InternalFailure and should count the miss.
func (t AWSTable) Lookup(c Code, k Kind) (entry AWSEntry, found bool) {
	if byKind, ok := t.ByKind[c]; ok {
		if e, ok := byKind[k]; ok {
			return e, true
		}
	}
	if e, ok := t.Default[c]; ok {
		return e, true
	}
	return InternalFailure, false
}

// Missing returns the codes that have no default entry. A surface test asserts it is empty,
// so a new Code cannot ship without a mapping.
func (t AWSTable) Missing() []Code {
	var out []Code
	for _, c := range Codes() {
		if _, ok := t.Default[c]; !ok {
			out = append(out, c)
		}
	}
	return out
}
