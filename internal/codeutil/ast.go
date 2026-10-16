package codeutil

import (
	"fmt"
	"go/ast"
	"strings"
)

// BuildSignature formats a Go function declaration as a compact signature.
func BuildSignature(function *ast.FuncDecl) string {
	var result strings.Builder
	if function.Recv != nil && len(function.Recv.List) > 0 {
		result.WriteString(fmt.Sprintf("func (%s) %s(", ReceiverName(function.Recv.List[0]), function.Name.Name))
	} else {
		result.WriteString(fmt.Sprintf("func %s(", function.Name.Name))
	}

	if function.Type.Params != nil {
		parameters := make([]string, 0, len(function.Type.Params.List))
		for _, parameter := range function.Type.Params.List {
			typeName := TypeString(parameter.Type)
			if len(parameter.Names) == 0 {
				parameters = append(parameters, typeName)
				continue
			}
			names := make([]string, len(parameter.Names))
			for index, name := range parameter.Names {
				names[index] = name.Name
			}
			parameters = append(parameters, strings.Join(names, ", ")+" "+typeName)
		}
		result.WriteString(strings.Join(parameters, ", "))
	}
	result.WriteString(")")

	if function.Type.Results != nil && len(function.Type.Results.List) > 0 {
		results := make([]string, 0, len(function.Type.Results.List))
		for _, field := range function.Type.Results.List {
			typeName := TypeString(field.Type)
			if len(field.Names) == 0 {
				results = append(results, typeName)
				continue
			}
			names := make([]string, len(field.Names))
			for index, name := range field.Names {
				names[index] = name.Name
			}
			results = append(results, strings.Join(names, ", ")+" "+typeName)
		}
		if len(results) == 1 {
			result.WriteString(" " + results[0])
		} else {
			result.WriteString(" (" + strings.Join(results, ", ") + ")")
		}
	}
	return result.String()
}

// TypeString formats the common Go AST type expressions used by code tools.
func TypeString(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return "*" + TypeString(value.X)
	case *ast.ArrayType:
		if value.Len == nil {
			return "[]" + TypeString(value.Elt)
		}
		return "[...]" + TypeString(value.Elt)
	case *ast.MapType:
		return "map[" + TypeString(value.Key) + "]" + TypeString(value.Value)
	case *ast.SelectorExpr:
		return TypeString(value.X) + "." + value.Sel.Name
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func"
	case *ast.ChanType:
		switch value.Dir {
		case ast.RECV:
			return "<-chan " + TypeString(value.Value)
		case ast.SEND:
			return "chan<- " + TypeString(value.Value)
		default:
			return "chan " + TypeString(value.Value)
		}
	case *ast.Ellipsis:
		return "..." + TypeString(value.Elt)
	default:
		return fmt.Sprintf("<%T>", expression)
	}
}

// ReceiverName formats a method receiver type.
func ReceiverName(field *ast.Field) string {
	switch value := field.Type.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		if identifier, ok := value.X.(*ast.Ident); ok {
			return "*" + identifier.Name
		}
	}
	return "?"
}
