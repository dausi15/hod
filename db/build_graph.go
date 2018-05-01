package db

import (
	"container/list"

	sparql "github.com/gtfierro/hod/lang/ast"
	"github.com/gtfierro/hod/turtle"
	"github.com/pkg/errors"
	"github.com/syndtr/goleveldb/leveldb"
)

// make sure to call this after we've populated the entity and publickey databases
// via db.LoadDataset
// This function builds the graph structure inside another leveldb kv store
// This is done in several passes (which we can optimize later):
//
// First pass:
//  - loop through all the triples and add the entities to the graph kv
//  - during this, we:
//	  - make a small local cache of predicateBytes => uint32 hash
//    - allocate an entity for both the subject AND object of a triple and add those
//      if they are not already added.
//		Make sure to use the entity/pk databases to look up their hashes (db.GetHash)
// Second pass:
//  - fill in all of the edges in the graph
func (db *DB) buildGraph(dataset turtle.DataSet) error {
	var predicates = make(map[string]Key)
	var subjAdded = 0
	var objAdded = 0
	var predAdded = 0
	graphtx, err := db.graphDB.OpenTransaction()
	if err != nil {
		return errors.Wrap(err, "Could not open transaction on graph dataset")
	}
	// first pass
	for _, triple := range dataset.Triples {
		// populate predicate cache
		if _, found := predicates[triple.Predicate.String()]; !found {
			predHash, err := db.GetHash(triple.Predicate)
			if err != nil {
				graphtx.Discard()
				return err
			}
			predicates[triple.Predicate.String()] = predHash
		}
		if reversePredicate, hasReverse := db.relationships[triple.Predicate]; hasReverse {
			if _, found := predicates[reversePredicate.String()]; !found {
				predHash, err := db.GetHash(reversePredicate)
				if err != nil {
					graphtx.Discard()
					return err
				}
				predicates[reversePredicate.String()] = predHash
			}
		}

		// make subject entity
		subjHash, err := db.GetHash(triple.Subject)
		if err != nil {
			graphtx.Discard()
			return err
		}
		// check if entity exists
		if exists, err := graphtx.Has(subjHash[:], nil); err == nil && !exists {
			// if not exists, create a new entity and insert it
			subjAdded += 1
			subEnt := NewEntity()
			subEnt.PK = subjHash
			bytes, err := subEnt.MarshalMsg(nil)
			if err != nil {
				graphtx.Discard()
				return err
			}
			if err := graphtx.Put(subjHash[:], bytes, nil); err != nil {
				graphtx.Discard()
				return err
			}
		} else if err != nil {
			graphtx.Discard()
			return err
		}

		// make object entity
		objHash, err := db.GetHash(triple.Object)
		if err != nil {
			graphtx.Discard()
			return err
		}
		// check if entity exists
		if exists, err := graphtx.Has(objHash[:], nil); err == nil && !exists {
			// if not exists, create a new entity and insert it
			objAdded += 1
			objEnt := NewEntity()
			objEnt.PK = objHash
			bytes, err := objEnt.MarshalMsg(nil)
			if err != nil {
				graphtx.Discard()
				return err
			}
			if err := graphtx.Put(objHash[:], bytes, nil); err != nil {
				graphtx.Discard()
				return err
			}
		} else if err != nil {
			graphtx.Discard()
			return err
		}

		// make predicate entity
		predHash, err := db.GetHash(triple.Predicate)
		if err != nil {
			graphtx.Discard()
			return err
		}
		// check if entity exists
		if exists, err := graphtx.Has(predHash[:], nil); err == nil && !exists {
			// if not exists, create a new entity and insert it
			predAdded += 1
			predEnt := NewEntity()
			predEnt.PK = predHash
			bytes, err := predEnt.MarshalMsg(nil)
			if err != nil {
				graphtx.Discard()
				return err
			}
			if err := graphtx.Put(predHash[:], bytes, nil); err != nil {
				graphtx.Discard()
				return err
			}
		} else if err != nil {
			graphtx.Discard()
			return err
		}
	}

	log.Noticef("ADDED subjects %d, predicates %d, objects %d", subjAdded, predAdded, objAdded)

	// second pass
	for _, triple := range dataset.Triples {
		subject, err := db.GetEntityTx(graphtx, triple.Subject)
		if err != nil {
			graphtx.Discard()
			return err
		}
		object, err := db.GetEntityTx(graphtx, triple.Object)
		if err != nil {
			graphtx.Discard()
			return err
		}

		// add the forward edge
		predHash := predicates[triple.Predicate.String()]
		subject.AddOutEdge(predHash, object.PK)
		object.AddInEdge(predHash, subject.PK)

		// find the inverse edge
		reverseEdge, hasReverseEdge := db.relationships[triple.Predicate]
		// if an inverse edge exists, then we add it to the object
		if hasReverseEdge {
			reverseEdgeHash := predicates[reverseEdge.String()]
			object.AddOutEdge(reverseEdgeHash, subject.PK)
			subject.AddInEdge(reverseEdgeHash, object.PK)
		}

		// re-put in graph
		bytes, err := subject.MarshalMsg(nil)
		if err != nil {
			graphtx.Discard()
			return err
		}
		if err := graphtx.Put(subject.PK[:], bytes, nil); err != nil {
			graphtx.Discard()
			return err
		}

		bytes, err = object.MarshalMsg(nil)
		if err != nil {
			graphtx.Discard()
			return err
		}
		if err := graphtx.Put(object.PK[:], bytes, nil); err != nil {
			graphtx.Discard()
			return err
		}
	}
	if err = graphtx.Commit(); err != nil {
		return errors.Wrap(err, "Could not commit transaction")
	}

	log.Notice("Starting third pass")

	// third pass
	extendtx, err := db.extendedDB.OpenTransaction()
	if err != nil {
		return errors.Wrap(err, "Could not open transaction on extended DB")
	}
	snap, err := db.snapshot()
	if err != nil {
		return errors.Wrap(err, "Could not open snapshot for pred index")
	}
	defer snap.Close()
	for predicate, predent := range db.predIndex {
		if err != nil {
			extendtx.Discard()
			return errors.Wrap(err, "Could not open transaction on extended index")
		}
		if err := db.populateIndex(snap, predicate, predent, extendtx); err != nil {
			extendtx.Discard()
			return err
		}
	}
	if err = extendtx.Commit(); err != nil {
		return errors.Wrap(err, "Could not commit transaction")
	}
	return nil
}

func (db *DB) populateIndex(snap *snapshot, predicateURI turtle.URI, predicate *PredicateEntity, extendtx *leveldb.Transaction) error {
	forwardPath := sparql.PathPattern{Pattern: sparql.PATTERN_ONE_PLUS}
	results := newKeyTree()
	if _, found := db.transitiveEdges[predicateURI]; !found {
		return nil
	}
	forwardPath.Predicate = predicateURI
	extendedPred := snap.MustGetHash(predicateURI)
	for subjectStringHash := range predicate.Subjects {
		var subjectHash Key
		subjectHash.FromSlice([]byte(subjectStringHash))
		if exists, err := extendtx.Has(subjectHash[:], nil); err == nil && !exists {
			subjectIndex := NewEntityExtendedIndex()
			subjectIndex.PK = subjectHash
			bytes, err := subjectIndex.MarshalMsg(nil)
			if err != nil {
				return err
			}
			if err := extendtx.Put(subjectHash[:], bytes, nil); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		subjectIndex, err := db.GetEntityIndexFromHashTx(extendtx, subjectHash)
		if err != nil {
			return err
		}

		subject, err := snap.GetEntityFromHash(subjectHash)
		if err != nil {
			return err
		}
		stack := list.New()
		snap.followPathFromSubject(subject, results, stack, forwardPath)
		//log.Debug(db.MustGetURI(subjectHash).Value, predicate.Value, results.Len())
		for results.Len() > 0 {
			subjectIndex.AddOutPlusEdge(extendedPred, results.DeleteMax())
		}
		bytes, err := subjectIndex.MarshalMsg(nil)
		if err != nil {
			return err
		}
		if err := extendtx.Put(subjectIndex.PK[:], bytes, nil); err != nil {
			return err
		}
	}
	for objectStringHash := range predicate.Objects {
		var objectHash Key
		objectHash.FromSlice([]byte(objectStringHash))
		if exists, err := extendtx.Has(objectHash[:], nil); err == nil && !exists {
			objectIndex := NewEntityExtendedIndex()
			objectIndex.PK = objectHash
			bytes, err := objectIndex.MarshalMsg(nil)
			if err != nil {
				return err
			}
			if err := extendtx.Put(objectHash[:], bytes, nil); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		objectIndex, err := db.GetEntityIndexFromHashTx(extendtx, objectHash)
		if err != nil {
			return err
		}

		object, err := snap.GetEntityFromHash(objectHash)
		if err != nil {
			return err
		}
		stack := list.New()
		snap.followPathFromObject(object, results, stack, forwardPath)
		for results.Len() > 0 {
			objectIndex.AddInPlusEdge(extendedPred, results.DeleteMax())
		}
		bytes, err := objectIndex.MarshalMsg(nil)
		if err != nil {
			return err
		}
		if err := extendtx.Put(objectIndex.PK[:], bytes, nil); err != nil {
			return err
		}
	}

	//TODO: need to make this in a transaction
	if reversePredicate, hasReverse := db.relationships[predicateURI]; hasReverse {
		forwardPath.Predicate = reversePredicate
		extendedPred = snap.MustGetHash(predicateURI)
		for subjectStringHash := range predicate.Subjects {
			var subjectHash Key
			subjectHash.FromSlice([]byte(subjectStringHash))
			if exists, err := extendtx.Has(subjectHash[:], nil); err == nil && !exists {
				subjectIndex := NewEntityExtendedIndex()
				subjectIndex.PK = subjectHash
				bytes, err := subjectIndex.MarshalMsg(nil)
				if err != nil {
					return err
				}
				if err := extendtx.Put(subjectHash[:], bytes, nil); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			subjectIndex, err := db.GetEntityIndexFromHashTx(extendtx, subjectHash)
			if err != nil {
				return err
			}

			subject, err := snap.GetEntityFromHash(subjectHash)
			if err != nil {
				return err
			}
			stack := list.New()
			snap.followPathFromSubject(subject, results, stack, forwardPath)
			for results.Len() > 0 {
				subjectIndex.AddInPlusEdge(extendedPred, results.DeleteMax())
			}
			bytes, err := subjectIndex.MarshalMsg(nil)
			if err != nil {
				return err
			}
			if err := extendtx.Put(subjectIndex.PK[:], bytes, nil); err != nil {
				return err
			}
		}
		for objectStringHash := range predicate.Objects {
			var objectHash Key
			objectHash.FromSlice([]byte(objectStringHash))
			if exists, err := extendtx.Has(objectHash[:], nil); err == nil && !exists {
				objectIndex := NewEntityExtendedIndex()
				objectIndex.PK = objectHash
				bytes, err := objectIndex.MarshalMsg(nil)
				if err != nil {
					return err
				}
				if err := extendtx.Put(objectHash[:], bytes, nil); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			objectIndex, err := db.GetEntityIndexFromHashTx(extendtx, objectHash)
			if err != nil {
				return err
			}

			object, err := snap.GetEntityFromHash(objectHash)
			if err != nil {
				return err
			}
			stack := list.New()
			snap.followPathFromObject(object, results, stack, forwardPath)
			for results.Len() > 0 {
				objectIndex.AddOutPlusEdge(extendedPred, results.DeleteMax())
			}
			bytes, err := objectIndex.MarshalMsg(nil)
			if err != nil {
				return err
			}
			if err := extendtx.Put(objectIndex.PK[:], bytes, nil); err != nil {
				return err
			}
		}
	}

	return nil
}
