package db

import (
	"fmt"

	sparql "github.com/gtfierro/hod/lang/ast"
	"github.com/pkg/errors"
)

const (
	RESOLVED   = "RESOLVED"
	UNRESOLVED = ""
)

// need operator types that go into the query plan
// Types:
//  SELECT: given a 2/3 triple, it resolves the 3rd item
//  FILTER: given a 1/3 triple, it restricts the other 2 items

// the old "queryplan" file is really a dependency graph for the query: it is NOT
// the queryplanner. What we should do now is take that dependency graph and turn
// it into a query plan

func (db *DB) formQueryPlan(dg *dependencyGraph, q *sparql.Query) (*queryPlan, error) {
	qp := newQueryPlan(dg, q)

	for _, term := range dg.terms {
		var (
			subjectIsVariable = term.Subject.IsVariable()
			objectIsVariable  = term.Object.IsVariable()
			// for now just look at first item in path
			predicateIsVariable  = term.Predicates[0].Predicate.IsVariable()
			subjectVar           = term.Subject.String()
			objectVar            = term.Object.String()
			predicateVar         = term.Predicates[0].Predicate.String()
			hasResolvedSubject   bool
			hasResolvedObject    bool
			hasResolvedPredicate bool
			newop                operation
			numvars              = len(term.variables)
		)
		hasResolvedSubject = qp.hasVar(subjectVar)
		hasResolvedObject = qp.hasVar(objectVar)
		hasResolvedPredicate = qp.hasVar(predicateVar)

		switch {
		// definitions: do these first
		case numvars == 1 && subjectIsVariable:
			newop = &resolveSubject{term: term}
			if !qp.varIsChild(subjectVar) {
				qp.addTopLevel(subjectVar)
			}
		case numvars == 1 && objectIsVariable:
			// s p ?o
			newop = &resolveObject{term: term}
			if !qp.varIsChild(objectVar) {
				qp.addTopLevel(objectVar)
			}
		case numvars == 1 && predicateIsVariable:
			// s ?p o
			newop = &resolvePredicate{term: term}
			if !qp.varIsChild(predicateVar) {
				qp.addTopLevel(predicateVar)
			}
		// terms with 3 variables
		case subjectIsVariable && objectIsVariable && predicateIsVariable:
			switch {
			case hasResolvedSubject:
				newop = &resolveVarTripleFromSubject{term: term}
			case hasResolvedObject:
				newop = &resolveVarTripleFromObject{term: term}
			case hasResolvedPredicate:
				newop = &resolveVarTripleFromPredicate{term: term}
			default: // all are vars
				newop = &resolveVarTripleAll{term: term}
			}
		// subject/object variable terms
		case subjectIsVariable && objectIsVariable && !predicateIsVariable:
			switch {
			case hasResolvedSubject && hasResolvedObject:
				// if we have both subject and object, we filter
				rso := &restrictSubjectObjectByPredicate{term: term}
				if qp.varIsChild(subjectVar) {
					qp.addLink(subjectVar, objectVar)
					rso.parentVar = subjectVar
					rso.childVar = objectVar
				} else if qp.varIsChild(objectVar) {
					qp.addLink(objectVar, subjectVar)
					rso.parentVar = objectVar
					rso.childVar = subjectVar
				} else if qp.varIsTop(subjectVar) {
					qp.addLink(subjectVar, objectVar)
					rso.parentVar = subjectVar
					rso.childVar = objectVar
				} else if qp.varIsTop(objectVar) {
					qp.addLink(objectVar, subjectVar)
					rso.parentVar = objectVar
					rso.childVar = subjectVar
				}
				newop = rso
			case hasResolvedObject:
				newop = &resolveSubjectFromVarObject{term: term}
				qp.addLink(objectVar, subjectVar)
			case hasResolvedSubject:
				newop = &resolveObjectFromVarSubject{term: term}
				qp.addLink(subjectVar, objectVar)
			default:
				newop = &resolveSubjectObjectFromPred{term: term}
				qp.addLink(subjectVar, objectVar)
			}
		case !subjectIsVariable && !objectIsVariable && predicateIsVariable:
			newop = &resolvePredicate{term: term}
			if !qp.varIsChild(predicateVar) {
				qp.addTopLevel(predicateVar)
			}
		case subjectIsVariable && !objectIsVariable && predicateIsVariable:
			// ?s ?p o
			newop = &resolveSubjectPredFromObject{term: term}
			qp.addLink(subjectVar, predicateVar)
		case !subjectIsVariable && objectIsVariable && predicateIsVariable:
			// s ?p ?o
			newop = &resolvePredObjectFromSubject{term: term}
			qp.addLink(objectVar, predicateVar)
		case subjectIsVariable:
			// ?s p o
			newop = &resolveSubject{term: term}
			if !qp.varIsChild(subjectVar) {
				qp.addTopLevel(subjectVar)
			}
		case objectIsVariable:
			// s p ?o
			newop = &resolveObject{term: term}
			if !qp.varIsChild(objectVar) {
				qp.addTopLevel(objectVar)
			}
		default:
			return qp, errors.New(fmt.Sprintf("Nothing chosen for %s. This shouldn't happen", term))
		}
		qp.operations = append(qp.operations, newop)
	}
	// sort operations
	// sort.Sort(qp)
	return qp, nil
}

// contains all useful state information for executing a query
type queryPlan struct {
	operations []operation
	selectVars []string
	dg         *dependencyGraph
	query      *sparql.Query
	vars       map[string]string
}

func newQueryPlan(dg *dependencyGraph, q *sparql.Query) *queryPlan {
	plan := &queryPlan{
		selectVars: dg.selectVars,
		dg:         dg,
		query:      q,
		vars:       make(map[string]string),
	}
	return plan
}

func (qp *queryPlan) dumpVarchain() {
	for k, v := range qp.vars {
		fmt.Println(k, "=>", v)
	}
}

func (plan *queryPlan) hasVar(variable string) bool {
	return plan.vars[variable] != UNRESOLVED
}

func (plan *queryPlan) varIsChild(variable string) bool {
	return plan.hasVar(variable) && plan.vars[variable] != RESOLVED
}

func (plan *queryPlan) varIsTop(variable string) bool {
	return plan.hasVar(variable) && plan.vars[variable] == RESOLVED
}

func (plan *queryPlan) addTopLevel(variable string) {
	plan.vars[variable] = RESOLVED
}

func (plan *queryPlan) addLink(parent, child string) {
	plan.vars[child] = parent
}
