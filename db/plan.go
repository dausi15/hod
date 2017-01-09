package db

import (
	"sort"
)

// need operator types that go into the query plan
// Types:
//  SELECT: given a 2/3 triple, it resolves the 3rd item
//  FILTER: given a 1/3 triple, it restricts the other 2 items

// the old "queryplan" file is really a dependency graph for the query: it is NOT
// the queryplanner. What we should do now is take that dependency graph and turn
// it into a query plan

func (db *DB) formQueryPlan(dg *dependencyGraph) *queryPlan {
	qp := newQueryPlan(dg)

	for term := range dg.iter() {
		var (
			subjectIsVariable = term.Subject.IsVariable()
			objectIsVariable  = term.Object.IsVariable()
			// for now just look at first item in path
			predicateIsVariable  = term.Path[0].Predicate.IsVariable()
			subjectVar           = term.Subject.String()
			objectVar            = term.Object.String()
			predicateVar         = term.Path[0].Predicate.String()
			hasResolvedSubject   bool
			hasResolvedObject    bool
			hasResolvedPredicate bool
			newop                operation
		)
		hasResolvedSubject = qp.hasVar(subjectVar)
		hasResolvedObject = qp.hasVar(objectVar)
		hasResolvedPredicate = qp.hasVar(predicateVar)

		switch {
		case subjectIsVariable && objectIsVariable && predicateIsVariable:
			// Cases:
			// NONE resolved: enumerate all triples in the store
			// subject, pred resolved:
			// object, pred resolved:
			// subject, object resolved:
			// subject resolved:
			// object resolved:
			// pred resolved:
			switch {
			case !hasResolvedSubject && !hasResolvedObject && !hasResolvedPredicate:
				log.Fatal("?x ?y ?z queries not supported yet")
			case !hasResolvedSubject && !hasResolvedObject && hasResolvedPredicate:
			case !hasResolvedSubject && hasResolvedObject && !hasResolvedPredicate:
			case !hasResolvedSubject && hasResolvedObject && hasResolvedPredicate:
			case hasResolvedSubject && !hasResolvedObject && !hasResolvedPredicate:
			case hasResolvedSubject && !hasResolvedObject && hasResolvedPredicate:
			case hasResolvedSubject && hasResolvedObject && !hasResolvedPredicate:
			case hasResolvedSubject && hasResolvedObject && hasResolvedPredicate:
			}
		case subjectIsVariable && objectIsVariable && !predicateIsVariable:
			switch {
			case hasResolvedSubject && hasResolvedObject:
				// if we have both subject and object, we filter
				rso := &restrictSubjectObjectByPredicate{term: term}
				subDepth := qp.findVarDepth(subjectVar)
				objDepth := qp.findVarDepth(objectVar)
				if subDepth > objDepth {
					qp.addLink(subjectVar, objectVar)
					rso.parentVar = subjectVar
					rso.childVar = objectVar
				} else if objDepth > subDepth {
					qp.addLink(objectVar, subjectVar)
					rso.parentVar = objectVar
					rso.childVar = subjectVar
				} else if qp.varIsChild(subjectVar) {
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
				panic("HERE")
			}
		case !subjectIsVariable && !objectIsVariable && predicateIsVariable:
			newop = &resolvePredicate{term: term}
			if !qp.varIsChild(predicateVar) {
				qp.addTopLevel(predicateVar)
			}
		case subjectIsVariable && !objectIsVariable && predicateIsVariable:
			newop = &resolveSubjectPredFromObject{term: term}
			//qp.addTopLevel(subjectVar)
			qp.addLink(subjectVar, predicateVar)
			//log.Fatal("?x ?y z query not supported yet")
		case !subjectIsVariable && objectIsVariable && predicateIsVariable:
			log.Fatal("x ?y ?z query not supported yet")
		case subjectIsVariable:
			newop = &resolveSubject{term: term}
			if !qp.varIsChild(subjectVar) {
				qp.addTopLevel(subjectVar)
			}
		case objectIsVariable:
			newop = &resolveObject{term: term}
			if !qp.varIsChild(objectVar) {
				qp.addTopLevel(objectVar)
			}
		default:
			log.Fatal("Nothing chosen for", term)
		}
		qp.operations = append(qp.operations, newop)
	}
	// sort operations
	sort.Sort(qp)
	return qp
}
