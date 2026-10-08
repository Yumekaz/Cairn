package api

// ValidResourceName keeps names usable as one URL/path component. Names are
// not filesystem paths; accepting separators lets volume/deploy writes escape
// their configured state directories.
func ValidResourceName(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for index, character := range name {
		alphanumeric := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
		if !alphanumeric && (index == 0 || character != '_' && character != '-' && character != '.') {
			return false
		}
	}
	return true
}
