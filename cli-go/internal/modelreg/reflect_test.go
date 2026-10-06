package modelreg

import "reflect"

// reflectFieldCount is the number of fields of a struct value; used to pin the
// shape of a type that must not grow (ProjectPolicy).
func reflectFieldCount(v any) int { return reflect.TypeOf(v).NumField() }
