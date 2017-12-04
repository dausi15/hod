package ast

import (
	"fmt"
	turtle "github.com/gtfierro/hod/goraptor"
	"github.com/gtfierro/hod/lang/token"
	"github.com/kr/pretty"
)

var debug = false

func SetDebug() {
	debug = true
}
func ClearDebug() {
	debug = false
}

type Query struct {
	Select    SelectClause
	Count     CountClause
	Where     WhereClause
	Variables []string
}

func NewQuery(selectclause, countclause, whereclause interface{}) (Query, error) {
	if debug {
		fmt.Printf("%# v", pretty.Formatter(whereclause.(WhereClause)))
	}
	q := Query{
		Where: whereclause.(WhereClause),
	}
	if selectclause != nil {
		q.Select = selectclause.(SelectClause)
	} else if countclause != nil {
		q.Count = countclause.(CountClause)
	}
	vars := make(map[string]int)
	// get all variables
	for _, triple := range q.Where.Terms {
		AddIfVar(triple.Subject, vars)
		AddIfVar(triple.Object, vars)
		for _, path := range triple.Predicates {
			AddIfVar(path.Predicate, vars)
		}
	}
	VarsFromGroup(q.Where.GraphGroup, vars)
	for varname := range vars {
		q.Variables = append(q.Variables, varname)
	}
	return q, nil
}

func AddIfVar(uri turtle.URI, m map[string]int) {
	if uri.IsVariable() {
		m[uri.String()] = 1
	}
}

func VarsFromGroup(group GraphGroup, m map[string]int) {
	for _, triple := range group.Terms {
		AddIfVar(triple.Subject, m)
		AddIfVar(triple.Object, m)
		for _, path := range triple.Predicates {
			AddIfVar(path.Predicate, m)
		}
	}
	for _, union := range group.Unions {
		VarsFromGroup(union, m)
	}
}

type SelectClause struct {
	Vars    []string
	AllVars bool
}

func NewAllSelectClause() (SelectClause, error) {
	return SelectClause{AllVars: true}, nil
}

func NewSelectClause(varlist interface{}) (SelectClause, error) {
	return SelectClause{Vars: varlist.([]string)}, nil
}

type CountClause struct {
	Vars    []string
	AllVars bool
}

func NewAllCountClause() (CountClause, error) {
	return CountClause{AllVars: true}, nil
}

func NewCountClause(varlist interface{}) (CountClause, error) {
	return CountClause{Vars: varlist.([]string)}, nil
}

type WhereClause struct {
	Terms      []Triple
	GraphGroup GraphGroup
}

func NewWhereClause(triples interface{}) (WhereClause, error) {
	return WhereClause{
		Terms: triples.([]Triple),
	}, nil
}

func NewWhereClauseWithGraphGroup(triples, group interface{}) (WhereClause, error) {
	return WhereClause{
		Terms:      triples.([]Triple),
		GraphGroup: group.(GraphGroup),
	}, nil
}

func NewWhereClauseGraphGroup(group interface{}) (WhereClause, error) {
	return WhereClause{
		GraphGroup: group.(GraphGroup),
	}, nil
}

type GraphGroup struct {
	Terms  []Triple
	Unions []GraphGroup
}

func GraphGroupFromTriples(triples interface{}) (GraphGroup, error) {
	return GraphGroup{
		Terms: triples.([]Triple),
	}, nil
}

func GraphGroupUnion(left, right interface{}) (GraphGroup, error) {
	return GraphGroup{
		Unions: []GraphGroup{left.(GraphGroup), right.(GraphGroup)},
	}, nil
}

func AddTriplesToGraphGroup(left, triples interface{}) (GraphGroup, error) {
	return GraphGroup{
		Terms:  append(left.(GraphGroup).Terms, triples.([]Triple)...),
		Unions: left.(GraphGroup).Unions,
	}, nil
}

func MergeGraphGroups(left, right interface{}) (GraphGroup, error) {
	return GraphGroup{
		Terms:  append(left.(GraphGroup).Terms, right.(GraphGroup).Terms...),
		Unions: append(left.(GraphGroup).Unions, right.(GraphGroup).Unions...),
	}, nil
}

type Triple struct {
	Subject    turtle.URI
	Predicates []PathPattern
	Object     turtle.URI
}

func NewTriple(subject, predicates, object interface{}) (Triple, error) {
	return Triple{
		Subject:    subject.(turtle.URI),
		Predicates: predicates.([]PathPattern),
		Object:     object.(turtle.URI),
	}, nil
}

func NewTripleBlock(triple interface{}) ([]Triple, error) {
	return []Triple{triple.(Triple)}, nil
}

func AppendTripleBlock(block, triple interface{}) ([]Triple, error) {
	return append(block.([]Triple), triple.(Triple)), nil
}

func NewURI(value interface{}) (turtle.URI, error) {
	return turtle.ParseURI(value.(string)), nil
}

func NewVarList(_var interface{}) ([]string, error) {
	return []string{_var.(string)}, nil
}

func AppendVar(varlist, _var interface{}) ([]string, error) {
	return append(varlist.([]string), _var.(string)), nil
}

func ParseString(_var interface{}) (string, error) {
	return string(_var.(*token.Token).Lit), nil
}

func NewPathSequence(_pred interface{}) ([]PathPattern, error) {
	return []PathPattern{_pred.(PathPattern)}, nil
}

func AppendPathSequence(_seq, _pred interface{}) ([]PathPattern, error) {
	return append(_seq.([]PathPattern), _pred.(PathPattern)), nil
}

func NewPathPattern(_pred interface{}) (PathPattern, error) {
	pred, _ := ParseString(_pred)
	return PathPattern{
		Predicate: turtle.ParseURI(pred),
		Pattern:   PATTERN_SINGLE,
	}, nil
}

func AddPathMod(_pred, _mod interface{}) (PathPattern, error) {
	return PathPattern{
		Predicate: _pred.(PathPattern).Predicate,
		Pattern:   _mod.(Pattern),
	}, nil
}

type PathPattern struct {
	Predicate turtle.URI
	Pattern   Pattern
}

func PathFromVar(_var interface{}) ([]PathPattern, error) {
	return []PathPattern{
		PathPattern{
			Predicate: turtle.ParseURI(_var.(string)),
			Pattern:   PATTERN_SINGLE,
		},
	}, nil
}

func (pp PathPattern) String() string {
	return pp.Predicate.String() + pp.Pattern.String()
}

type Pattern uint

const (
	PATTERN_SINGLE = iota + 1
	PATTERN_ZERO_ONE
	PATTERN_ONE_PLUS
	PATTERN_ZERO_PLUS
)

func (p Pattern) String() string {
	switch p {
	case PATTERN_SINGLE:
		return ""
	case PATTERN_ZERO_ONE:
		return "?"
	case PATTERN_ONE_PLUS:
		return "+"
	case PATTERN_ZERO_PLUS:
		return "*"
	}
	return "unknown"
}
