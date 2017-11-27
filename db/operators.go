// this file contains the set of query operators generated by the query planner
package db

import (
	"fmt"

	"github.com/gtfierro/hod/query"
	"github.com/pkg/errors"
	"github.com/syndtr/goleveldb/leveldb"
)

type operation interface {
	run(ctx *queryContext) error
	String() string
	SortKey() string
	GetTerm() *queryTerm
}

// ?subject predicate object
// Find all subjects part of triples with the given predicate and object
type resolveSubject struct {
	term *queryTerm
}

func (rs *resolveSubject) String() string {
	return fmt.Sprintf("[resolveSubject %s]", rs.term)
}

func (rs *resolveSubject) SortKey() string {
	return rs.term.Subject.String()
}

func (rs *resolveSubject) GetTerm() *queryTerm {
	return rs.term
}

func (rs *resolveSubject) run(ctx *queryContext) error {
	// fetch the object from the graph
	object, err := ctx.db.GetHash(rs.term.Object)
	if err != nil && err != leveldb.ErrNotFound {
		return errors.Wrap(err, fmt.Sprintf("%+v", rs.term))
	} else if err == leveldb.ErrNotFound {
		return nil
	}
	subjectVar := rs.term.Subject.String()
	// get all subjects reachable from the given object along the path
	subjects := ctx.db.getSubjectFromPredObject(object, rs.term.Path)

	if !ctx.defined(subjectVar) {
		// if not defined, then we put this into the relation
		ctx.defineVariable(subjectVar, subjects)
		ctx.rel.add1Value(subjectVar, subjects)
	} else {
		// if it *is* already defined, then we intersect the values by joining
		ctx.unionDefinitions(subjectVar, subjects)

		newrel := NewRelation([]string{subjectVar})
		newrel.add1Value(subjectVar, subjects)

		ctx.rel.join(newrel, []string{subjectVar}, ctx)
	}

	return nil
}

// object predicate ?object
// Find all objects part of triples with the given predicate and subject
type resolveObject struct {
	term *queryTerm
}

func (ro *resolveObject) String() string {
	return fmt.Sprintf("[resolveObject %s]", ro.term)
}

func (ro *resolveObject) SortKey() string {
	return ro.term.Object.String()
}

func (ro *resolveObject) GetTerm() *queryTerm {
	return ro.term
}

func (ro *resolveObject) run(ctx *queryContext) error {
	// fetch the subject from the graph
	subject, err := ctx.db.GetHash(ro.term.Subject)
	if err != nil && err != leveldb.ErrNotFound {
		return errors.Wrap(err, fmt.Sprintf("%+v", ro.term))
	} else if err == leveldb.ErrNotFound {
		return nil
	}
	objectVar := ro.term.Object.String()
	objects := ctx.db.getObjectFromSubjectPred(subject, ro.term.Path)

	if !ctx.defined(objectVar) {
		ctx.defineVariable(objectVar, objects)
		ctx.rel.add1Value(objectVar, objects)
	} else {
		ctx.unionDefinitions(objectVar, objects)

		newrel := NewRelation([]string{objectVar})
		newrel.add1Value(objectVar, objects)

		ctx.rel.join(newrel, []string{objectVar}, ctx)
	}

	return nil
}

// object ?predicate object
// Find all predicates part of triples with the given subject and subject
type resolvePredicate struct {
	term *queryTerm
}

func (op *resolvePredicate) String() string {
	return fmt.Sprintf("[resolvePredicate %s]", op.term)
}

func (op *resolvePredicate) SortKey() string {
	return op.term.Path[0].Predicate.String()
}

func (op *resolvePredicate) GetTerm() *queryTerm {
	return op.term
}

func (op *resolvePredicate) run(ctx *queryContext) error {
	// fetch the subject from the graph
	subject, err := ctx.db.GetEntity(op.term.Subject)
	if err != nil && err != leveldb.ErrNotFound {
		return errors.Wrap(err, fmt.Sprintf("%+v", op.term))
	} else if err == leveldb.ErrNotFound {
		return nil
	}
	// now get object
	object, err := ctx.db.GetEntity(op.term.Object)
	if err != nil && err != leveldb.ErrNotFound {
		return errors.Wrap(err, fmt.Sprintf("%+v", op.term))
	} else if err == leveldb.ErrNotFound {
		return nil
	}

	predicateVar := op.term.Path[0].Predicate.String()
	// get all preds w/ the given end object, starting from the given subject

	predicates := ctx.db.getPredicateFromSubjectObject(subject, object)

	// new stuff
	if !ctx.defined(predicateVar) {
		ctx.defineVariable(predicateVar, predicates)
		ctx.rel.add1Value(predicateVar, predicates)
	} else {
		ctx.unionDefinitions(predicateVar, predicates)

		newrel := NewRelation([]string{predicateVar})
		newrel.add1Value(predicateVar, predicates)

		ctx.rel.join(newrel, []string{predicateVar}, ctx)
	}

	return nil
}

// ?sub pred ?obj
// Find all subjects and objects that have the given relationship
type restrictSubjectObjectByPredicate struct {
	term                *queryTerm
	parentVar, childVar string
}

func (rso *restrictSubjectObjectByPredicate) String() string {
	return fmt.Sprintf("[restrictSubObjByPred %s]", rso.term)
}

func (rso *restrictSubjectObjectByPredicate) SortKey() string {
	return rso.parentVar
}

func (rso *restrictSubjectObjectByPredicate) GetTerm() *queryTerm {
	return rso.term
}

func (rso *restrictSubjectObjectByPredicate) run(ctx *queryContext) error {
	var (
		subjectVar = rso.term.Subject.String()
		objectVar  = rso.term.Object.String()
	)

	// this operator takes existing values for subjects and objects and finds the pairs of them that
	// are connected by the path defined by rso.term.Path.

	var rsop_relation *Relation
	var relation_contents [][]Key
	var joinOn []string

	// use whichever variable has already been joined on, which means
	// that there are values in the relation that we can join with
	if ctx.hasJoined(subjectVar) {
		joinOn = []string{subjectVar}
		subjects := ctx.getValuesForVariable(subjectVar)

		rsop_relation = NewRelation([]string{subjectVar, objectVar})

		subjects.Iter(func(subject Key) {
			reachableObjects := ctx.db.getObjectFromSubjectPred(subject, rso.term.Path)
			// we restrict the values in reachableObjects to those that we already have inside 'objectVar'
			ctx.restrictToResolved(objectVar, reachableObjects)
			reachableObjects.Iter(func(objectKey Key) {
				relation_contents = append(relation_contents, []Key{subject, objectKey})
			})
		})
		rsop_relation.add2Values(subjectVar, objectVar, relation_contents)

	} else if ctx.hasJoined(objectVar) {
		joinOn = []string{objectVar}
		objects := ctx.getValuesForVariable(objectVar)

		rsop_relation = NewRelation([]string{objectVar, subjectVar})

		objects.Iter(func(object Key) {
			reachableSubjects := ctx.db.getSubjectFromPredObject(object, rso.term.Path)
			ctx.restrictToResolved(subjectVar, reachableSubjects)

			reachableSubjects.Iter(func(subjectKey Key) {
				relation_contents = append(relation_contents, []Key{object, subjectKey})
			})
		})
		rsop_relation.add2Values(objectVar, subjectVar, relation_contents)
	} else if ctx.cardinalityUnique(subjectVar) < ctx.cardinalityUnique(objectVar) {
		// we start with whichever has fewer values (subject or object). For each of them, we search
		// the graph for reachable endpoints (object or subject) on the provided path (rso.term.Path)
		// neither is joined
		joinOn = []string{subjectVar}
		subjects := ctx.getValuesForVariable(subjectVar)

		rsop_relation = NewRelation([]string{subjectVar, objectVar})

		subjects.Iter(func(subject Key) {
			reachableObjects := ctx.db.getObjectFromSubjectPred(subject, rso.term.Path)
			ctx.restrictToResolved(objectVar, reachableObjects)

			reachableObjects.Iter(func(objectKey Key) {
				relation_contents = append(relation_contents, []Key{subject, objectKey})
			})
		})
		rsop_relation.add2Values(subjectVar, objectVar, relation_contents)
	} else {
		joinOn = []string{objectVar}
		objects := ctx.getValuesForVariable(objectVar)

		rsop_relation = NewRelation([]string{objectVar, subjectVar})

		objects.Iter(func(object Key) {
			reachableSubjects := ctx.db.getSubjectFromPredObject(object, rso.term.Path)
			ctx.restrictToResolved(subjectVar, reachableSubjects)

			reachableSubjects.Iter(func(subjectKey Key) {
				relation_contents = append(relation_contents, []Key{object, subjectKey})
			})
		})
		rsop_relation.add2Values(objectVar, subjectVar, relation_contents)
	}

	ctx.rel.join(rsop_relation, joinOn, ctx)
	ctx.markJoined(subjectVar)
	ctx.markJoined(objectVar)

	return nil
}

// ?sub pred ?obj, but we have already resolved the object
// For each of the current
type resolveSubjectFromVarObject struct {
	term *queryTerm
}

func (rsv *resolveSubjectFromVarObject) String() string {
	return fmt.Sprintf("[resolveSubFromVarObj %s]", rsv.term)
}

func (rsv *resolveSubjectFromVarObject) SortKey() string {
	return rsv.term.Object.String()
}

func (rsv *resolveSubjectFromVarObject) GetTerm() *queryTerm {
	return rsv.term
}

// Use this when we have subject and object variables, but only object has been filled in
func (rsv *resolveSubjectFromVarObject) run(ctx *queryContext) error {
	var (
		objectVar  = rsv.term.Object.String()
		subjectVar = rsv.term.Subject.String()
	)

	var rsop_relation = NewRelation([]string{objectVar, subjectVar})
	var relation_contents [][]Key

	newSubjects := newKeyTree()

	objects := ctx.getValuesForVariable(objectVar)
	objects.Iter(func(object Key) {
		reachableSubjects := ctx.db.getSubjectFromPredObject(object, rsv.term.Path)
		ctx.restrictToResolved(subjectVar, reachableSubjects)

		reachableSubjects.Iter(func(subjectKey Key) {
			newSubjects.Add(subjectKey)
			relation_contents = append(relation_contents, []Key{object, subjectKey})
		})
	})

	rsop_relation.add2Values(rsop_relation.keys[0], rsop_relation.keys[1], relation_contents)
	ctx.rel.join(rsop_relation, rsop_relation.keys[:1], ctx)
	ctx.markJoined(subjectVar)
	ctx.markJoined(objectVar)
	ctx.unionDefinitions(subjectVar, newSubjects)

	return nil
}

type resolveObjectFromVarSubject struct {
	term *queryTerm
}

func (rov *resolveObjectFromVarSubject) String() string {
	return fmt.Sprintf("[resolveObjFromVarSub %s]", rov.term)
}

func (rov *resolveObjectFromVarSubject) SortKey() string {
	return rov.term.Subject.String()
}

func (rov *resolveObjectFromVarSubject) GetTerm() *queryTerm {
	return rov.term
}

func (rov *resolveObjectFromVarSubject) run(ctx *queryContext) error {
	var (
		objectVar  = rov.term.Object.String()
		subjectVar = rov.term.Subject.String()
	)

	var rsop_relation = NewRelation([]string{subjectVar, objectVar})
	var relation_contents [][]Key

	subjects := ctx.getValuesForVariable(subjectVar)
	subjects.Iter(func(subject Key) {
		reachableObjects := ctx.db.getObjectFromSubjectPred(subject, rov.term.Path)
		ctx.restrictToResolved(objectVar, reachableObjects)
		reachableObjects.Iter(func(objectKey Key) {
			relation_contents = append(relation_contents, []Key{subject, objectKey})
		})
	})

	rsop_relation.add2Values(subjectVar, objectVar, relation_contents)
	ctx.rel.join(rsop_relation, rsop_relation.keys[:1], ctx)
	ctx.markJoined(subjectVar)
	ctx.markJoined(objectVar)

	return nil
}

type resolveObjectFromVarSubjectPred struct {
	term *queryTerm
}

func (op *resolveObjectFromVarSubjectPred) String() string {
	return fmt.Sprintf("[resolveObjFromVarSubPred %s]", op.term)
}

func (op *resolveObjectFromVarSubjectPred) SortKey() string {
	return op.term.Subject.String()
}

func (op *resolveObjectFromVarSubjectPred) GetTerm() *queryTerm {
	return op.term
}

// ?s ?p o
func (rov *resolveObjectFromVarSubjectPred) run(ctx *queryContext) error {
	return nil
}

type resolveSubjectObjectFromPred struct {
	term *queryTerm
}

func (op *resolveSubjectObjectFromPred) String() string {
	return fmt.Sprintf("[resolveSubObjFromPred %s]", op.term)
}

func (op *resolveSubjectObjectFromPred) SortKey() string {
	return op.term.Subject.String()
}

func (op *resolveSubjectObjectFromPred) GetTerm() *queryTerm {
	return op.term
}

func (rso *resolveSubjectObjectFromPred) run(ctx *queryContext) error {
	subsobjs := ctx.db.getSubjectObjectFromPred(rso.term.Path)
	subjectVar := rso.term.Subject.String()
	objectVar := rso.term.Object.String()

	ctx.rel.add2Values(subjectVar, objectVar, subsobjs)
	ctx.markJoined(subjectVar)
	ctx.markJoined(objectVar)

	return nil
}

type resolveSubjectPredFromObject struct {
	term *queryTerm
}

func (op *resolveSubjectPredFromObject) String() string {
	return fmt.Sprintf("[resolveSubPredFromObj %s]", op.term)
}

func (op *resolveSubjectPredFromObject) SortKey() string {
	return op.term.Path[0].Predicate.String()
}

func (op *resolveSubjectPredFromObject) GetTerm() *queryTerm {
	return op.term
}

// we have an object and want to find subjects/predicates that connect to it.
// If we have partially resolved the predicate, then we iterate through those connected to
// the known object and then pull the associated subjects. We then filter those subjects
// by anything we've already resolved.
// If we have *not* resolved the predicate, then this is easy: just graph traverse from the object
func (op *resolveSubjectPredFromObject) run(ctx *queryContext) error {
	subjectVar := op.term.Subject.String()
	predicateVar := op.term.Path[0].Predicate.String()

	// fetch the object from the graph
	object, err := ctx.db.GetEntity(op.term.Object)
	if err != nil && err != leveldb.ErrNotFound {
		return errors.Wrap(err, fmt.Sprintf("%+v", op.term))
	} else if err == leveldb.ErrNotFound {
		return nil
	}

	// get all predicates from it
	predicates := ctx.db.getPredicatesFromObject(object)

	var sub_pred_pairs [][]Key
	predicates.Iter(func(predicate Key) {
		if !ctx.validValue(predicateVar, predicate) {
			return
		}
		path := []query.PathPattern{{Predicate: ctx.db.MustGetURI(predicate), Pattern: query.PATTERN_SINGLE}}
		subjects := ctx.db.getSubjectFromPredObject(object.PK, path)

		subjects.Iter(func(subject Key) {
			if !ctx.validValue(subjectVar, subject) {
				return
			}
			sub_pred_pairs = append(sub_pred_pairs, []Key{subject, predicate})

		})
	})

	if ctx.defined(subjectVar) {
		rsop_relation := NewRelation([]string{subjectVar, predicateVar})
		rsop_relation.add2Values(subjectVar, predicateVar, sub_pred_pairs)
		ctx.rel.join(rsop_relation, []string{subjectVar}, ctx)
	} else if ctx.defined(predicateVar) {
		rsop_relation := NewRelation([]string{subjectVar, predicateVar})
		rsop_relation.add2Values(subjectVar, predicateVar, sub_pred_pairs)
		ctx.rel.join(rsop_relation, []string{predicateVar}, ctx)
	} else {
		ctx.rel.add2Values(subjectVar, predicateVar, sub_pred_pairs)
	}

	return nil
}

type resolvePredObjectFromSubject struct {
	term *queryTerm
}

func (op *resolvePredObjectFromSubject) String() string {
	return fmt.Sprintf("[resolvePredObjectFromSubject %s]", op.term)
}

func (op *resolvePredObjectFromSubject) SortKey() string {
	return op.term.Path[0].Predicate.String()
}

func (op *resolvePredObjectFromSubject) GetTerm() *queryTerm {
	return op.term
}

func (op *resolvePredObjectFromSubject) run(ctx *queryContext) error {
	objectVar := op.term.Object.String()
	predicateVar := op.term.Path[0].Predicate.String()

	// fetch the subject from the graph
	subject, err := ctx.db.GetEntity(op.term.Subject)
	if err != nil && err != leveldb.ErrNotFound {
		return errors.Wrap(err, fmt.Sprintf("%+v", op.term))
	} else if err == leveldb.ErrNotFound {
		return nil
	}

	// get all predicates from it
	predicates := ctx.db.getPredicatesFromSubject(subject)

	var pred_obj_pairs [][]Key
	predicates.Iter(func(predicate Key) {
		if !ctx.validValue(predicateVar, predicate) {
			return
		}
		path := []query.PathPattern{{Predicate: ctx.db.MustGetURI(predicate), Pattern: query.PATTERN_SINGLE}}
		objects := ctx.db.getObjectFromSubjectPred(subject.PK, path)

		objects.Iter(func(object Key) {
			if !ctx.validValue(objectVar, object) {
				return
			}
			pred_obj_pairs = append(pred_obj_pairs, []Key{predicate, object})
		})
	})

	ctx.rel.add2Values(predicateVar, objectVar, pred_obj_pairs)

	return nil
}

// TODO: implement these for ?s ?p ?o constructs
// TODO: also requires query planner
type resolveVarTripleFromSubject struct {
	term *queryTerm
}

func (op *resolveVarTripleFromSubject) String() string {
	return fmt.Sprintf("[resolveVarTripleFromSubject %s]", op.term)
}

func (op *resolveVarTripleFromSubject) SortKey() string {
	return op.term.Subject.String()
}

func (op *resolveVarTripleFromSubject) GetTerm() *queryTerm {
	return op.term
}

// ?s ?p ?o; start from s
func (op *resolveVarTripleFromSubject) run(ctx *queryContext) error {
	// for all subjects, find all predicates and objects. Note: these predicates
	// and objects may be partially evaluated already
	var (
		subjectVar   = op.term.Subject.String()
		objectVar    = op.term.Object.String()
		predicateVar = op.term.Path[0].Predicate.String()
	)

	var rsop_relation = NewRelation([]string{subjectVar, predicateVar, objectVar})
	var relation_contents [][]Key

	subjects := ctx.definitions[subjectVar]
	subjects.Iter(func(subjectKey Key) {
		var predKey Key
		subject := ctx.db.MustGetEntityFromHash(subjectKey)
		for edge, objectList := range subject.OutEdges {
			predKey.FromSlice([]byte(edge))
			for _, objectKey := range objectList {
				relation_contents = append(relation_contents, []Key{subject.PK, predKey, objectKey})
			}
		}
	})

	rsop_relation.add3Values(subjectVar, predicateVar, objectVar, relation_contents)
	ctx.rel.join(rsop_relation, []string{subjectVar}, ctx)
	ctx.markJoined(subjectVar)
	return nil
}

type resolveVarTripleFromObject struct {
	term *queryTerm
}

func (op *resolveVarTripleFromObject) String() string {
	return fmt.Sprintf("[resolveVarTripleFromObject %s]", op.term)
}

func (op *resolveVarTripleFromObject) SortKey() string {
	return op.term.Object.String()
}

func (op *resolveVarTripleFromObject) GetTerm() *queryTerm {
	return op.term
}

// ?s ?p ?o; start from o
func (op *resolveVarTripleFromObject) run(ctx *queryContext) error {
	var (
		subjectVar   = op.term.Subject.String()
		objectVar    = op.term.Object.String()
		predicateVar = op.term.Path[0].Predicate.String()
	)

	var rsop_relation = NewRelation([]string{objectVar, predicateVar, subjectVar})
	var relation_contents [][]Key

	objects := ctx.definitions[objectVar]
	objects.Iter(func(objectKey Key) {
		var predKey Key
		object := ctx.db.MustGetEntityFromHash(objectKey)
		for edge, subjectList := range object.InEdges {
			predKey.FromSlice([]byte(edge))
			for _, subjectKey := range subjectList {
				relation_contents = append(relation_contents, []Key{object.PK, predKey, subjectKey})
			}
		}
	})

	rsop_relation.add3Values(objectVar, predicateVar, subjectVar, relation_contents)
	ctx.rel.join(rsop_relation, []string{objectVar}, ctx)
	ctx.markJoined(objectVar)
	return nil
}

type resolveVarTripleFromPredicate struct {
	term *queryTerm
}

func (op *resolveVarTripleFromPredicate) String() string {
	return fmt.Sprintf("[resolveVarTripleFromPredicate %s]", op.term)
}

func (op *resolveVarTripleFromPredicate) SortKey() string {
	return op.term.Path[0].Predicate.String()
}

func (op *resolveVarTripleFromPredicate) GetTerm() *queryTerm {
	return op.term
}

// ?s ?p ?o; start from p
func (op *resolveVarTripleFromPredicate) run(ctx *queryContext) error {
	var (
		subjectVar   = op.term.Subject.String()
		objectVar    = op.term.Object.String()
		predicateVar = op.term.Path[0].Predicate.String()
	)

	var rsop_relation = NewRelation([]string{predicateVar, subjectVar, objectVar})
	var relation_contents [][]Key

	predicates := ctx.definitions[predicateVar]
	predicates.Iter(func(predicateKey Key) {
		var subjectKey Key
		// TODO: use?
		// subsobjs := ctx.db.getSubjectObjectFromPred(rso.term.Path)
		uri := ctx.db.MustGetURI(predicateKey)
		predicate := ctx.db.predIndex[uri]
		for subStrHash, subjectMap := range predicate.Subjects {
			copy(subjectKey[:], []byte(subStrHash))
			for objStrHash := range subjectMap {
				var objectHash Key
				objectHash.FromSlice([]byte(objStrHash))
				relation_contents = append(relation_contents, []Key{predicateKey, subjectKey, objectHash})
			}
		}
	})

	ctx.markJoined(predicateVar)
	rsop_relation.add3Values(predicateVar, subjectVar, objectVar, relation_contents)
	ctx.rel.join(rsop_relation, []string{predicateVar}, ctx)
	ctx.markJoined(predicateVar)
	return nil

}

type resolveVarTripleAll struct {
	term *queryTerm
}

func (op *resolveVarTripleAll) String() string {
	return fmt.Sprintf("[resolveVarTripleAll %s]", op.term)
}

func (op *resolveVarTripleAll) SortKey() string {
	return op.term.Subject.String()
}

func (op *resolveVarTripleAll) GetTerm() *queryTerm {
	return op.term
}

// ?s ?p ?o; start from s
func (op *resolveVarTripleAll) run(ctx *queryContext) error {
	return nil
}
