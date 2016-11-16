package db

import (
	"container/list"
	"encoding/binary"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/btree"
	query "github.com/gtfierro/hod/query"
)

// make a set of structs that capture what these queries want to do

// So, how do queries work?
// We have a list of filters, each of which has a subject, list of predicate things, and
// an object. Any of these might be variables, which we can distinguish by having a "?"
// in front of the value.
//
// First we "clean" these by making sure that they have their full
// namespaces rather than the prefix

type Item [4]byte

func (i Item) Less(than btree.Item) bool {
	t := than.(Item)
	return binary.LittleEndian.Uint32(i[:]) < binary.LittleEndian.Uint32(t[:])
}

type queryRun struct {
	plan      *queryPlan
	variables map[string]*btree.BTree
}

func makeQueryRun(plan *queryPlan) *queryRun {
	qr := &queryRun{
		plan:      plan,
		variables: make(map[string]*btree.BTree),
	}
	for _, v := range plan.selectVars {
		qr.variables[v] = btree.New(3)
	}
	return qr
}

// struct to hold the graph of the query plan
type queryPlan struct {
	selectVars []string
	roots      []*queryTerm
	// map of variable name -> resolved?
	variables map[string]bool
}

// initializes the query plan struct
func makeQueryPlan(q query.Query) *queryPlan {
	qp := &queryPlan{
		selectVars: []string{},
		roots:      []*queryTerm{},
		variables:  make(map[string]bool),
	}
	for _, v := range q.Select.Variables {
		qp.selectVars = append(qp.selectVars, v.String())
	}
	return qp
}

// returns true of the query plan or any of its children
// already includes the given query term
func (qp *queryPlan) hasChild(qt *queryTerm) bool {
	for _, r := range qp.roots {
		if r.equals(qt) {
			return true
		}
		if r.hasChild(qt) {
			return true
		}
	}
	return false
}

// adds the query term to the root set if it is
// not already there
func (qp *queryPlan) addRootTerm(qt *queryTerm) {
	if !qp.hasChild(qt) {
		// loop through and append to a node if we share a variable with it
		for _, root := range qp.roots {
			if root.bubbleDownDepends(qt) {
				return
			}
		}
		// otherwise, add it to the roots
		qp.roots = append(qp.roots, qt)
	}
}

func (qp *queryPlan) dump() {
	for _, r := range qp.roots {
		r.dump(0)
	}
}

// Firstly, if qt is already in the plan, we return
// iterate through in a breadth first search for any node
// [qt] shares a variable with. We attach qt as a child of that
// term
// Returns true if the node was added
func (qp *queryPlan) addChild(qt *queryTerm) bool {
	if qp.hasChild(qt) {
		fmt.Println("qp already has", qt.String())
		return false
	}
	stack := list.New()
	// push the roots onto the stack
	for _, r := range qp.roots {
		stack.PushFront(r)
	}
	for stack.Len() > 0 {
		node := stack.Remove(stack.Front()).(*queryTerm)
		// if depends on, attach and return
		if qt.dependsOn(node) {
			//fmt.Println("node", qt.String(), "depends on", node.String())
			node.children = append(node.children, qt)
			return true
		}
		// add node children to back of stack
		for _, c := range node.children {
			stack.PushBack(c)
		}
	}
	return false
}

// stores the state/variables for a particular triple
// from a SPARQL query
type queryTerm struct {
	query.Filter
	children  []*queryTerm
	qp        *queryPlan
	variables []string
}

// initializes a queryTerm from a given Filter
func (qp *queryPlan) makeQueryTerm(f query.Filter) *queryTerm {
	qt := &queryTerm{
		f,
		[]*queryTerm{},
		qp,
		[]string{},
	}
	// TODO: handle the predicates
	if qt.Subject.IsVariable() {
		qt.qp.variables[qt.Subject.String()] = false
		qt.variables = append(qt.variables, qt.Subject.String())
	}
	if qt.Object.IsVariable() {
		qt.qp.variables[qt.Object.String()] = false
		qt.variables = append(qt.variables, qt.Object.String())
	}
	return qt
}

// returns the number of unresolved variables in the term
func (qt *queryTerm) numUnresolved() int {
	num := 0
	for _, v := range qt.variables {
		if !qt.qp.variables[v] {
			num++
		}
	}
	return num
}

// returns true if the term or any of its children has
// the given child
func (qt *queryTerm) hasChild(child *queryTerm) bool {
	for _, c := range qt.children {
		if c.equals(child) {
			return true
		}
		if c.hasChild(child) {
			return true
		}
	}
	return false
}

// returns true if two query terms are equal
func (qt *queryTerm) equals(qt2 *queryTerm) bool {
	return qt.Subject == qt2.Subject &&
		qt.Object == qt2.Object &&
		reflect.DeepEqual(qt.Path, qt2.Path)
}

func (qt *queryTerm) String() string {
	return fmt.Sprintf("<%s %s %s>", qt.Subject, qt.Path, qt.Object)
}

func (qt *queryTerm) dump(indent int) {
	fmt.Println(strings.Repeat("  ", indent), qt.String())
	for _, c := range qt.children {
		c.dump(indent + 1)
	}
}

func (qt *queryTerm) dependsOn(other *queryTerm) bool {
	for _, v := range qt.variables {
		for _, vv := range other.variables {
			if vv == v {
				return true
			}
		}
	}
	return false
}

// adds "other" as a child of the furthest node down the children tree
// that the node depends on. Returns true if "other" was added to the tree,
// and false otherwise
func (qt *queryTerm) bubbleDownDepends(other *queryTerm) bool {
	if !other.dependsOn(qt) {
		return false
	}
	for _, child := range qt.children {
		if other.bubbleDownDepends(child) {
			return true
		}
	}
	qt.children = append(qt.children, other)
	return true
}

// removes all terms in the removeList from removeFrom and returns
// the result
func filterTermList(removeFrom, removeList []*queryTerm) []*queryTerm {
	var ret = []*queryTerm{}
	for _, a := range removeFrom {
		keep := true
		for _, b := range removeList {
			if a.equals(b) {
				keep = false
				break
			}
		}
		if keep {
			ret = append(ret, a)
		}
	}
	return ret
}

func (db *DB) RunQuery(q query.Query) {
	// "clean" the query by expanding out the prefixes
	for idx, filter := range q.Where {
		if !strings.HasPrefix(filter.Subject.Value, "?") {
			if full, found := db.namespaces[filter.Subject.Namespace]; found {
				filter.Subject.Namespace = full
			}
			q.Where[idx] = filter
		}
		if !strings.HasPrefix(filter.Object.Value, "?") {
			if full, found := db.namespaces[filter.Object.Namespace]; found {
				filter.Object.Namespace = full
			}
			q.Where[idx] = filter
		}
		for idx2, pred := range filter.Path {
			if !strings.HasPrefix(pred.Predicate.Value, "?") {
				if full, found := db.namespaces[pred.Predicate.Namespace]; found {
					pred.Predicate.Namespace = full
				}
				filter.Path[idx2] = pred
			}
		}
		q.Where[idx] = filter
	}

	fmt.Println("-------------- start query plan -------------")
	planStart := time.Now()
	qp := db.formExecutionPlan(q)
	log.Infof("Formed execution plan in %s", time.Since(planStart))
	fmt.Println("-------------- end query plan -------------")

	runStart := time.Now()
	run := makeQueryRun(qp)
	db.executeQuery(run)
	log.Infof("Ran query in %s", time.Since(runStart))

	for _, varName := range q.Select.Variables {
		resultTree := run.variables[varName.String()]
		if q.Select.Count {
			fmt.Println(varName, resultTree.Len())
		} else {
			iter := func(i btree.Item) bool {
				uri := db.MustGetURI(i.(Item))
				fmt.Println(varName, uri.String())
				return i != resultTree.Max()
			}
			resultTree.Ascend(iter)
		}
	}
}

// We need an execution plan for the list of filters contained in a query. How do we do this?
func (db *DB) formExecutionPlan(q query.Query) *queryPlan {
	qp := makeQueryPlan(q)
	terms := make([]*queryTerm, len(q.Where))
	for i, f := range q.Where {
		terms[i] = qp.makeQueryTerm(f)
	}

	for len(terms) > 0 {
		// first find all the terms with 0 or 1 unresolved variable terms
		var added = []*queryTerm{}
		for _, term := range terms {
			if term.numUnresolved() < 2 {
				qp.addRootTerm(term)
				added = append(added, term)
			}
		}
		// remove the terms that we added to the root set
		terms = filterTermList(terms, added)
		added = []*queryTerm{}
		for _, term := range terms {
			if qp.addChild(term) {
				added = append(added, term)
			}
		}
		terms = filterTermList(terms, added)
	}
	qp.dump()
	return qp
}

// okay how do we run the execution plan?
func (db *DB) executeQuery(run *queryRun) {
	// first, resolve all the roots and store the intermediate results

	stack := list.New()
	for _, r := range run.plan.roots {
		stack.PushFront(r)
	}
	for stack.Len() > 0 {
		node := stack.Remove(stack.Front()).(*queryTerm)
		fmt.Println("pop", node)
		db.runFilterTerm(run, node)
		// add node children to back of stack
		for _, c := range node.children {
			stack.PushBack(c)
		}
	}
	for variable, res := range run.variables {
		fmt.Printf("var %s has count %d\n", variable, res.Len())
	}

}

func (db *DB) runFilterTerm(run *queryRun, term *queryTerm) error {
	var (
		subjectIsVariable = strings.HasPrefix(term.Subject.Value, "?")
		objectIsVariable  = strings.HasPrefix(term.Object.Value, "?")
	)
	if !subjectIsVariable && !objectIsVariable {
		log.Warningf("THIS IS WEIRD")
		return nil
		//log.Noticef("S/O anchored: S: %s, O: %s", term.Subject.String(), term.Object.String())
		//results := db.getSubjectObjectFromPred(term.Path[0])
		//log.Infof("Got %d results", len(results))
	} else if !subjectIsVariable {
		log.Noticef("S anchored: S: %s, O: %s", term.Subject.String(), term.Object.String())
		entity, err := db.GetEntity(term.Subject)
		if err != nil {
			return err
		}
		results := db.getObjectFromSubjectPred(entity.PK, term.Path)
		if tree, found := run.variables[term.Object.String()]; found {
			mergeTrees(tree, results)
		} else {
			tree := btree.New(3)
			mergeTrees(tree, results)
			run.variables[term.Object.String()] = tree
		}
	} else if !objectIsVariable {
		log.Noticef("O anchored: S: %s, O: %s", term.Subject.String(), term.Object.String())
		entity, err := db.GetEntity(term.Object)
		if err != nil {
			return err
		}

		results := db.getSubjectFromPredObject(entity.PK, term.Path)
		if tree, found := run.variables[term.Subject.String()]; found {
			mergeTrees(tree, results)
		} else {
			tree := btree.New(3)
			mergeTrees(tree, results)
			run.variables[term.Subject.String()] = tree
		}
	} else {
		// if both the subject and object are variables, then there are 4 scenarios:
		// 1: we have results for S but not O (e.g. S was a variable that we already have some results for)
		// 2. we have results for O but not S
		// 3. we have results for BOTH S and O
		// 4. we do NOT have results for either S or O
		// If scenario 4, then the query is not solveable, because if we are at this point,
		// then we should have filled at least one of the variables
		subTree, have_sub := run.variables[term.Subject.String()]
		objTree, have_obj := run.variables[term.Object.String()]
		if have_sub {
			have_sub = subTree.Len() > 0
		}
		if have_obj {
			have_obj = objTree.Len() > 0
		}
		log.Debug("have s?", have_sub, "have o?", have_obj)
		if have_sub && have_obj {
			log.Warning("NOT DONE YET")
		} else if have_obj {
			subTree = btree.New(3)
			iter := func(i btree.Item) bool {
				object, err := db.GetEntityFromHash(i.(Item))
				if err != nil {
					log.Error(err)
				}
				//TODO: change this to use compound paths
				predHash := db.predIndex[term.Path[0].Predicate]
				for _, s := range object.InEdges[string(predHash.PK[:])] {
					subTree.ReplaceOrInsert(Item(s))
				}
				return i != objTree.Max()
			}
			objTree.Ascend(iter)
			run.variables[term.Subject.String()] = subTree
		} else if have_sub {
			objTree = btree.New(3)
			iter := func(i btree.Item) bool {
				subject, err := db.GetEntityFromHash(i.(Item))
				if err != nil {
					log.Error(err)
				}
				// TODO: change to use compound predicates
				predHash := db.predIndex[term.Path[0].Predicate]
				for _, s := range subject.OutEdges[string(predHash.PK[:])] {
					subTree.ReplaceOrInsert(Item(s))
				}
				return i != subTree.Max()
			}
			subTree.Ascend(iter)
			run.variables[term.Subject.String()] = objTree
		} else {
			log.Warning("WHY ARE WE HERE")
		}
		log.Noticef("not anchored!: S: %s, O: %s", term.Subject.String(), term.Object.String())
	}
	return nil
}

// TODO: change to use compound predicates
// takes the inverse of every relationship. If no inverse exists, returns nil
func (db *DB) reversePathPattern(path []query.PathPattern) []query.PathPattern {
	var reverse = make([]query.PathPattern, len(path))
	for idx, pred := range path {
		if inverse, found := db.relationships[pred.Predicate]; found {
			pred.Predicate = inverse
			reverse[idx] = pred
		} else {
			return nil
		}
	}
	return reverse
}

// follow the pattern from the given object's InEdges, placing the results in the btree
func (db *DB) followPathFromObject(object *Entity, results *btree.BTree, searchstack *list.List, pattern query.PathPattern) {
	stack := list.New()
	stack.PushFront(object)

	predHash, err := db.GetHash(pattern.Predicate)
	if err != nil {
		panic(fmt.Errorf("Not found: %v (%s)", pattern.Predicate, err))
	}

	for stack.Len() > 0 {
		entity := stack.Remove(stack.Front()).(*Entity)
		switch pattern.Pattern {
		case query.PATTERN_SINGLE:
			// [found] indicates whether or not we have any edges with the given pattern
			edges, found := entity.InEdges[string(predHash[:])]
			// this requires the pattern to exist, so we skip if we have no edges of that name
			if !found {
				continue
			}
			// here, these entities are all connected by the required predicate
			for _, entityHash := range edges {
				nextEntity := db.MustGetEntityFromHash(entityHash)
				results.ReplaceOrInsert(Item(nextEntity.PK))
				searchstack.PushBack(nextEntity)
			}
			// because this is one hop, we don't add any new entities to the stack
		case query.PATTERN_ZERO_ONE:
			log.Notice("PATH ?", pattern)
			// this does not require the pattern to exist, so we add ALL entities connected
			// by ALL edges
			for _, endpointHashList := range entity.InEdges {
				for _, entityHash := range endpointHashList {
					nextEntity := db.MustGetEntityFromHash(entityHash)
					results.ReplaceOrInsert(Item(nextEntity.PK))
					searchstack.PushBack(nextEntity)
				}
			}
			// because this is one hop, we don't add any new entities to the stack
		case query.PATTERN_ZERO_PLUS:
			log.Notice("PATH *", pattern)
		case query.PATTERN_ONE_PLUS:
			edges, found := entity.InEdges[string(predHash[:])]
			// this requires the pattern to exist, so we skip if we have no edges of that name
			if !found {
				continue
			}
			// here, these entities are all connected by the required predicate
			for _, entityHash := range edges {
				nextEntity := db.MustGetEntityFromHash(entityHash)
				results.ReplaceOrInsert(Item(nextEntity.PK))
				searchstack.PushBack(nextEntity)
				// also make sure to add this to the stack so we can search
				stack.PushBack(nextEntity)
			}
		}
	}
}

// follow the pattern from the given subject's OutEdges, placing the results in the btree
func (db *DB) followPathFromSubject(subject *Entity, results *btree.BTree, searchstack *list.List, pattern query.PathPattern) {
	stack := list.New()
	stack.PushFront(subject)

	predHash, err := db.GetHash(pattern.Predicate)
	if err != nil {
		panic(err)
	}

	for stack.Len() > 0 {
		entity := stack.Remove(stack.Front()).(*Entity)
		switch pattern.Pattern {
		case query.PATTERN_SINGLE:
			// [found] indicates whether or not we have any edges with the given pattern
			edges, found := entity.OutEdges[string(predHash[:])]
			// this requires the pattern to exist, so we skip if we have no edges of that name
			if !found {
				continue
			}
			// here, these entities are all connected by the required predicate
			for _, entityHash := range edges {
				nextEntity := db.MustGetEntityFromHash(entityHash)
				results.ReplaceOrInsert(Item(nextEntity.PK))
				searchstack.PushBack(nextEntity)
			}
			// because this is one hop, we don't add any new entities to the stack
		case query.PATTERN_ZERO_ONE:
			// this does not require the pattern to exist, so we add ALL entities connected
			// by ALL edges
			for _, endpointHashList := range entity.OutEdges {
				for _, entityHash := range endpointHashList {
					nextEntity := db.MustGetEntityFromHash(entityHash)
					results.ReplaceOrInsert(Item(nextEntity.PK))
					searchstack.PushBack(nextEntity)
				}
			}
			// because this is one hop, we don't add any new entities to the stack
		case query.PATTERN_ZERO_PLUS:
		case query.PATTERN_ONE_PLUS:
			edges, found := entity.OutEdges[string(predHash[:])]
			// this requires the pattern to exist, so we skip if we have no edges of that name
			if !found {
				continue
			}
			// here, these entities are all connected by the required predicate
			for _, entityHash := range edges {
				nextEntity := db.MustGetEntityFromHash(entityHash)
				results.ReplaceOrInsert(Item(nextEntity.PK))
				searchstack.PushBack(nextEntity)
				// also make sure to add this to the stack so we can search
				stack.PushBack(nextEntity)
			}
		}
	}
}

func (db *DB) getSubjectFromPredObject(objectHash [4]byte, path []query.PathPattern) *btree.BTree {
	// first get the initial object entity from the db
	// then we're going to conduct a BFS search starting from this entity looking for all entities
	// that have the required path sequence. We place the results in a BTree to maintain uniqueness

	// So how does this traversal actually work?
	// At each 'step', we are looking at an entity and some offset into the path.

	// get the object, look in its "in" edges for the path pattern
	objEntity, err := db.GetEntityFromHash(objectHash)
	if err != nil {
		panic(err)
	}

	results := btree.New(2)

	stack := list.New()
	stack.PushFront(objEntity)

	for stack.Len() > 0 {
		entity := stack.Remove(stack.Front()).(*Entity)
		for _, pat := range path {
			db.followPathFromObject(entity, results, stack, pat)
		}
	}

	return results
}

// TODO: change to use compound predicates
// Given object and predicate, get all subjects
//func (db *DB) getSubjectFromPredObject(objectHash [4]byte, pattern query.PathPattern) [][4]byte {
//	// get the object, look in its "in" edges for the path pattern
//	objEntity, err := db.GetEntityFromHash(objectHash)
//	if err != nil {
//		panic(err)
//	}
//	// get predicate hash
//	predHash, err := db.GetHash(pattern.Predicate)
//	if err != nil {
//		panic(err)
//	}
//	return objEntity.InEdges[string(predHash[:])]
//}

// TODO: change to use compound predicates
// Given object and predicate, get all subjects
func (db *DB) getObjectFromSubjectPred(subjectHash [4]byte, path []query.PathPattern) *btree.BTree {
	subEntity, err := db.GetEntityFromHash(subjectHash)
	if err != nil {
		panic(err)
	}

	results := btree.New(2)

	stack := list.New()
	stack.PushFront(subEntity)

	for stack.Len() > 0 {
		entity := stack.Remove(stack.Front()).(*Entity)
		for _, pat := range path {
			db.followPathFromSubject(entity, results, stack, pat)
		}
	}

	return results
}

// TODO: change to use compound predicates
// Given a predicate, it returns pairs of (subject, object) that are connected by that relationship
func (db *DB) getSubjectObjectFromPred(pattern query.PathPattern) (soPair [][][4]byte) {
	pe, found := db.predIndex[pattern.Predicate]
	if !found {
		panic(fmt.Sprintf("Cannot find predicate %s", pattern.Predicate))
	}
	for subject, objectMap := range pe.Subjects {
		for object := range objectMap {
			var sh, oh [4]byte
			copy(sh[:], subject)
			copy(oh[:], object)
			soPair = append(soPair, [][4]byte{sh, oh})
		}
	}
	return soPair
}
