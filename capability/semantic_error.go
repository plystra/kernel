package capability

// MaximumSemanticErrorCodeSize bounds canonical semantic error codes.
const MaximumSemanticErrorCodeSize = 128

// SemanticError is the structural contract implemented by generated semantic
// error values. The Kernel accepts its code only when the exact typed contract
// declares that code.
type SemanticError interface {
	error
	SemanticErrorCode() string
}

// ValidSemanticErrorCode reports whether code is canonical lower snake case.
func ValidSemanticErrorCode(code string) bool {
	if code == "" || len(code) > MaximumSemanticErrorCodeSize || code[0] < 'a' || code[0] > 'z' {
		return false
	}
	previousUnderscore := false
	for index := 1; index < len(code); index++ {
		character := code[index]
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousUnderscore = false
		case character == '_' && !previousUnderscore:
			previousUnderscore = true
		default:
			return false
		}
	}
	return !previousUnderscore
}
