package generator

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
)

// returns an expression that casts jsValue to the given native type
// if the value cant be directly cast, a runtime resolver is also returned.
func (gen *generator) ResolveValue(
	name *ast.Ident,
	jsValue ast.Expr,
	nativeType ast.Expr,
	dst ast.Expr,
) (ast.Expr, []ast.Stmt, error) {
	switch nativeType := nativeType.(type) {
	case *ast.Ident:
		return gen.resolveIdent(name, jsValue, nativeType, dst)
	case *ast.StarExpr:
		return gen.resolvePointer(name, jsValue, nativeType, dst)
	case *ast.ArrayType:
		return gen.resolveArray(name, jsValue, nativeType, dst)
	case *ast.StructType:
		return gen.resolveStruct(name, jsValue, nativeType, dst)
	default:

		panic(fmt.Errorf("Unrecognized native type : %v", nativeType))
	}
}

func (gen *generator) resolveIdent(
	name *ast.Ident,
	jsValue ast.Expr,
	nativeType *ast.Ident,
	dst ast.Expr,
) (expr ast.Expr, resolver []ast.Stmt, err error) {
	var method, typeCast string
	switch typeStr := nativeType.String(); typeStr {
	case "bool":
		method = "Bool"
	case "string":
		method = "String"
	case "int", "int8", "int16", "int32", "rune", "int64",
		"uint", "uint8", "byte", "uint16", "uint32", "uint64", "uintptr":
		method = "Int"
		if typeStr != "int" {
			typeCast = typeStr
		}
	case "float32", "float64":
		method = "Float"
		if typeStr != "float64" {
			typeCast = typeStr
		}
	default:
		nativeType, err := gen.getTypeAlias(typeStr)
		if err != nil {
			return nil, nil, fmt.Errorf("Unresolved identifier: %v", err)
		}

		return gen.ResolveValue(&ast.Ident{Name: typeStr}, jsValue, nativeType, dst)
	}

	expr = &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   jsValue,
			Sel: &ast.Ident{Name: method},
		},
	}

	if typeCast != "" {
		expr = &ast.CallExpr{
			Fun:  &ast.Ident{Name: typeCast},
			Args: []ast.Expr{expr},
		}
	}

	if dst != nil {
		resolver = append(resolver, &ast.AssignStmt{
			Lhs: []ast.Expr{dst},
			Tok: token.ASSIGN,
			Rhs: []ast.Expr{expr},
		})

		expr = dst
	}

	return expr, resolver, err
}

func (gen *generator) resolvePointer(
	name *ast.Ident,
	jsValue ast.Expr,
	nativeType *ast.StarExpr,
	dst ast.Expr,
) (expr ast.Expr, resolver []ast.Stmt, err error) {
	if dst == nil {
		dst = name
		resolver = append(resolver, &ast.DeclStmt{
			Decl: &ast.GenDecl{
				Tok: token.VAR,
				Specs: []ast.Spec{
					&ast.ValueSpec{
						Names: []*ast.Ident{name},
						Type:  nativeType,
					},
				},
			},
		})
	}

	_, eltResolver, err := gen.ResolveValue(
		&ast.Ident{Name: name.Name + "Elt"},
		jsValue,
		nativeType.X,
		dst,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("Unresolved pointer element type %v: %v", nativeType.X, err)
	}

	return dst, append(
		resolver,
		&ast.IfStmt{
			Init: &ast.AssignStmt{
				Lhs: []ast.Expr{&ast.Ident{Name: "jsType"}},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{
					&ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   jsValue,
							Sel: &ast.Ident{Name: "Type"},
						},
					},
				},
			},
			Cond: &ast.BinaryExpr{
				X: &ast.BinaryExpr{
					X:  &ast.Ident{Name: "jsType"},
					Op: token.NEQ,
					Y: &ast.SelectorExpr{
						X:   &ast.Ident{Name: "js"},
						Sel: &ast.Ident{Name: "TypeUndefined"},
					},
				},
				Op: token.LOR,
				Y: &ast.BinaryExpr{
					X:  &ast.Ident{Name: "jsType"},
					Op: token.NEQ,
					Y: &ast.SelectorExpr{
						X:   &ast.Ident{Name: "js"},
						Sel: &ast.Ident{Name: "TypeNull"},
					},
				},
			},
			Body: &ast.BlockStmt{
				List: eltResolver,
			},
		},
	), err
}

func (gen *generator) resolveArray(
	name *ast.Ident,
	jsValue ast.Expr,
	nativeType *ast.ArrayType,
	dst ast.Expr,
) (expr ast.Expr, resolver []ast.Stmt, err error) {
	lenExpr := nativeType.Len
	if lenExpr == nil { // if the native type represents a slice
		// create a variable to hold the runtime length
		lenExpr = &ast.Ident{Name: name.Name + "Len"}

		// resolve the runtime length
		resolver = append(resolver, &ast.AssignStmt{
			Lhs: []ast.Expr{lenExpr},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{
				&ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   jsValue,
						Sel: &ast.Ident{Name: "Length"},
					},
				},
			},
		})
	}

	if dst == nil {
		if nativeType.Len == nil {
			// declare a new slice using make and add it to the resolver
			resolver = append(resolver, &ast.AssignStmt{
				Lhs: []ast.Expr{name},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{
					&ast.CallExpr{
						Fun:  &ast.Ident{Name: "make"},
						Args: []ast.Expr{nativeType, lenExpr},
					},
				},
			})
		} else {
			// declare a new array and add it to the resolver
			resolver = append(resolver, &ast.DeclStmt{
				Decl: &ast.GenDecl{
					Tok: token.VAR,
					Specs: []ast.Spec{
						&ast.ValueSpec{
							Names: []*ast.Ident{name},
							Type:  nativeType,
						},
					},
				},
			})
		}

		// set dst to the newly declared destination
		dst = name
	}

	idxIdent := &ast.Ident{Name: name.Name + "Idx"}
	_, eltResolver, err := gen.ResolveValue(
		&ast.Ident{Name: name.Name + "Elt"},
		&ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   jsValue,
				Sel: &ast.Ident{Name: "Index"},
			},
			Args: []ast.Expr{idxIdent},
		},
		nativeType.Elt,
		&ast.IndexExpr{X: dst, Index: idxIdent},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("Unresolved array element type %v: %v", nativeType.Elt, err)
	}

	return dst, append(
		resolver,
		&ast.ForStmt{
			Init: &ast.AssignStmt{
				Lhs: []ast.Expr{idxIdent},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{
					&ast.BasicLit{
						Kind:  token.INT,
						Value: "0",
					},
				},
			},
			Cond: &ast.BinaryExpr{
				X:  idxIdent,
				Op: token.LSS,
				Y:  lenExpr,
			},
			Post: &ast.IncDecStmt{
				X:   idxIdent,
				Tok: token.INC,
			},
			Body: &ast.BlockStmt{
				List: eltResolver,
			},
		},
	), err
}

func (gen *generator) resolveStruct(
	name *ast.Ident,
	jsValue ast.Expr,
	nativeType *ast.StructType,
	dst ast.Expr,
) (expr ast.Expr, resolver []ast.Stmt, err error) {
	if dst == nil {
		resolver = append(resolver, &ast.AssignStmt{
			Lhs: []ast.Expr{name},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{
				&ast.CompositeLit{Type: nativeType},
			},
		})

		dst = name
	}

	for _, field := range nativeType.Fields.List {
		for _, fieldName := range field.Names {
			_, fieldResolver, err := gen.ResolveValue(
				&ast.Ident{Name: name.Name + fieldName.Name},
				&ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   jsValue,
						Sel: &ast.Ident{Name: "Get"},
					},
					Args: []ast.Expr{fieldName},
				},
				field.Type,
				&ast.SelectorExpr{
					X:   dst,
					Sel: fieldName,
				},
			)
			if err != nil {
				return nil, nil, fmt.Errorf("Unresolved struct field type %v: %v", field.Type, err)
			}

			resolver = append(resolver, fieldResolver...)
		}
	}

	return dst, resolver, err
}

func (gen *generator) resolveFuncArgs(params *ast.FieldList) (args []ast.Expr, resolver []ast.Stmt, err error) {
	var i int
	args = make([]ast.Expr, params.NumFields())
	resolvers := make([]ast.Stmt, 0)

	for _, param := range params.List {
		for _, name := range param.Names {
			args[i], resolver, err = gen.ResolveValue(
				name,
				&ast.IndexExpr{
					X: &ast.Ident{Name: "args"},
					Index: &ast.BasicLit{
						Kind:  token.INT,
						Value: strconv.Itoa(i),
					},
				},
				param.Type,
				nil,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("Unresolved argument \"%s\" type %v: %v", name, param.Type, err)
			}

			if resolver != nil {
				resolvers = append(resolvers, resolver...)
			}

			i++
		}
	}

	return args, resolvers, err
}
