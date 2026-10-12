package errs

// Code classifies an error. The set and the order are the one console-api already uses
// (resource.Code), so existing mappings carry over. Unspecified is the zero value.
type Code int

// The error codes.
const (
	CodeUnspecified Code = iota
	CodeInvalidArgument
	CodeNotFound
	CodeAlreadyExists
	CodeConflict
	CodeIdempotencyMismatch
	CodeUnauthenticated
	CodePermissionDenied
	CodeResourceExhausted
	CodePayloadTooLarge
	CodeUnavailable
	CodeUnimplemented
	CodeInternal
)

var codeNames = map[Code]string{
	CodeUnspecified:         "Unspecified",
	CodeInvalidArgument:     "InvalidArgument",
	CodeNotFound:            "NotFound",
	CodeAlreadyExists:       "AlreadyExists",
	CodeConflict:            "Conflict",
	CodeIdempotencyMismatch: "IdempotencyMismatch",
	CodeUnauthenticated:     "Unauthenticated",
	CodePermissionDenied:    "PermissionDenied",
	CodeResourceExhausted:   "ResourceExhausted",
	CodePayloadTooLarge:     "PayloadTooLarge",
	CodeUnavailable:         "Unavailable",
	CodeUnimplemented:       "Unimplemented",
	CodeInternal:            "Internal",
}

// String returns the canonical name, such as "NotFound". An unknown value is "Unspecified".
func (c Code) String() string {
	if name, ok := codeNames[c]; ok {
		return name
	}
	return "Unspecified"
}

// Codes returns every code, Unspecified first. Tests use it to check that a table is complete.
func Codes() []Code {
	out := make([]Code, 0, len(codeNames))
	for c := CodeUnspecified; c <= CodeInternal; c++ {
		out = append(out, c)
	}
	return out
}

// Retryable reports whether a caller may repeat the request unchanged.
func (c Code) Retryable() bool { return c == CodeUnavailable }

// Kind names a sort of resource. The wire name is Kind.String. The set is console-api's
// resource.Kind.
type Kind int

// The resource kinds. KindUnspecified is the zero value.
const (
	KindUnspecified Kind = iota
	KindInstance
	KindImage
	KindReservation
	KindSubnet
	KindBucket
	KindObject
	KindOperation
	KindVpc
	KindVolume
	KindSecurityGroup
	KindKeyPair
	KindAddress
	KindSnapshot
)

var kindNames = map[Kind]string{
	KindInstance:      "instance",
	KindImage:         "image",
	KindReservation:   "reservation",
	KindSubnet:        "subnet",
	KindBucket:        "bucket",
	KindObject:        "object",
	KindOperation:     "operation",
	KindVpc:           "vpc",
	KindVolume:        "volume",
	KindSecurityGroup: "securityGroup",
	KindKeyPair:       "keyPair",
	KindAddress:       "address",
	KindSnapshot:      "snapshot",
}

// String returns the wire name. KindUnspecified and an unknown kind return "".
func (k Kind) String() string { return kindNames[k] }
