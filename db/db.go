package db

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gtfierro/hod/config"
	turtle "github.com/gtfierro/hod/goraptor"
	"github.com/gtfierro/hod/query"

	"github.com/blevesearch/bleve"
	"github.com/coocood/freecache"
	"github.com/op/go-logging"
	"github.com/pkg/errors"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/filter"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/tinylib/msgp/msgp"
	"github.com/zhangxinngang/murmur"
)

// logger
var log *logging.Logger
var emptyKey = Key{0, 0, 0, 0}

func init() {
	log = logging.MustGetLogger("hod")
	var format = "%{color}%{level} %{shortfile} %{time:Jan 02 15:04:05} %{color:reset} ▶ %{message}"
	var logBackend = logging.NewLogBackend(os.Stderr, "", 0)
	logBackendLeveled := logging.AddModuleLevel(logBackend)
	logging.SetBackend(logBackendLeveled)
	logging.SetFormatter(logging.MustStringFormatter(format))
}

type DB struct {
	path string
	// store []byte(entity URI) => primary key
	entityDB *leveldb.DB
	// store primary key => [](entity URI)
	pkDB *leveldb.DB
	// predicate index: stores "children" of predicates
	predDB    *leveldb.DB
	predIndex map[turtle.URI]*PredicateEntity
	// graph structure
	graphDB *leveldb.DB
	// extended index DB
	extendedDB *leveldb.DB
	// store relationships and their inverses
	relationships map[turtle.URI]turtle.URI
	// stores which edges can be 'rolled forward' in the index
	transitiveEdges map[turtle.URI]struct{}
	// store the namespace prefixes as strings
	namespaces map[string]string
	// cache for entity hashes
	entityHashCache   *freecache.Cache
	entityObjectCache map[Key]*Entity
	eocLock           sync.RWMutex
	entityIndexCache  map[Key]*EntityExtendedIndex
	eicLock           sync.RWMutex
	uriCache          map[Key]turtle.URI
	uriLock           sync.RWMutex
	// config options for output
	showDependencyGraph    bool
	showQueryPlan          bool
	showQueryPlanLatencies bool
	showOperationLatencies bool
	showQueryLatencies     bool
	// cache for query results
	queryCache        *freecache.Cache
	queryCacheEnabled bool
	loading           bool

	// text index
	textidx bleve.Index
}

func NewDB(cfg *config.Config) (*DB, error) {
	path := strings.TrimSuffix(cfg.DBPath, "/")
	logging.SetLevel(cfg.LogLevel, "hod")

	options := &opt.Options{
		Filter: filter.NewBloomFilter(32),
	}

	// set up entity, pk databases
	entityDBPath := path + "/db-entities"
	entityDB, err := leveldb.OpenFile(entityDBPath, options)
	if err != nil {
		return nil, errors.Wrapf(err, "Could not open entityDB file %s", entityDBPath)
	}

	pkDBPath := path + "/db-pk"
	pkDB, err := leveldb.OpenFile(pkDBPath, options)
	if err != nil {
		return nil, errors.Wrapf(err, "Could not open pkDB file %s", pkDBPath)
	}

	// set up entity, pk databases
	extendedDBPath := path + "/db-extended"
	extendedDB, err := leveldb.OpenFile(extendedDBPath, options)
	if err != nil {
		return nil, errors.Wrapf(err, "Could not open extendedDB file %s", extendedDBPath)
	}

	graphDBPath := path + "/db-graph"
	graphDB, err := leveldb.OpenFile(graphDBPath, options)
	if err != nil {
		return nil, errors.Wrapf(err, "Could not open graphDB file %s", graphDBPath)
	}
	predDBPath := path + "/db-pred"
	predDB, err := leveldb.OpenFile(predDBPath, options)
	if err != nil {
		return nil, errors.Wrapf(err, "Could not open predDB file %s", predDBPath)
	}

	mapping := bleve.NewIndexMapping()
	var index bleve.Index
	index, err = bleve.New(path+"/myExampleIndex.bleve", mapping)
	if err != nil && err == bleve.ErrorIndexPathExists {
		index, err = bleve.Open(path + "/myExampleIndex.bleve")
	}
	if err != nil {
		return nil, errors.Wrapf(err, "Could not open bleve index %s", path+"/myExampleIndex.bleve")
	}

	db := &DB{
		path:                   path,
		entityDB:               entityDB,
		extendedDB:             extendedDB,
		pkDB:                   pkDB,
		graphDB:                graphDB,
		predDB:                 predDB,
		predIndex:              make(map[turtle.URI]*PredicateEntity),
		relationships:          make(map[turtle.URI]turtle.URI),
		transitiveEdges:        make(map[turtle.URI]struct{}),
		namespaces:             make(map[string]string),
		showDependencyGraph:    cfg.ShowDependencyGraph,
		showQueryPlan:          cfg.ShowQueryPlan,
		showQueryPlanLatencies: cfg.ShowQueryPlanLatencies,
		showOperationLatencies: cfg.ShowOperationLatencies,
		showQueryLatencies:     cfg.ShowQueryLatencies,
		entityHashCache:        freecache.NewCache(16 * 1024 * 1024), // 16 MB
		entityObjectCache:      make(map[Key]*Entity),
		entityIndexCache:       make(map[Key]*EntityExtendedIndex),
		uriCache:               make(map[Key]turtle.URI),
		queryCacheEnabled:      !cfg.DisableQueryCache,
		loading:                false,
		textidx:                index,
	}

	if db.queryCacheEnabled {
		db.queryCache = freecache.NewCache(64 * 1024 * 1024) // 64 MB
	}

	// load predIndex and relationships from database
	predIndexPath := path + "/predIndex"
	relshipIndexPath := path + "/relshipIndex"
	namespaceIndexPath := path + "/namespaceIndex"
	if _, err := os.Stat(predIndexPath); !os.IsNotExist(err) {
		f, err := os.Open(predIndexPath)
		if err != nil {
			return nil, errors.Wrapf(err, "Could not open predIndex file %s", predIndexPath)
		}
		var pi = new(PredIndex)
		if err := msgp.Decode(f, pi); err != nil {
			return nil, err
		}
		for uri, pe := range *pi {
			db.predIndex[turtle.ParseURI(uri)] = pe
		}
	}
	if _, err := os.Stat(relshipIndexPath); !os.IsNotExist(err) {
		f, err := os.Open(relshipIndexPath)
		if err != nil {
			return nil, errors.Wrapf(err, "Could not open relshipIndexPath file %s", relshipIndexPath)
		}
		var ri = new(RelshipIndex)
		if err := msgp.Decode(f, ri); err != nil {
			return nil, err
		}
		for uri, uri2 := range *ri {
			db.relationships[turtle.ParseURI(uri)] = turtle.ParseURI(uri2)
		}
	}
	if _, err := os.Stat(namespaceIndexPath); !os.IsNotExist(err) {
		f, err := os.Open(namespaceIndexPath)
		if err != nil {
			return nil, errors.Wrapf(err, "Could not open namespaceIndexPath file %s", namespaceIndexPath)
		}
		var ni = new(NamespaceIndex)
		if err := msgp.Decode(f, ni); err != nil {
			return nil, err
		}
		for ns, full := range *ni {
			db.namespaces[ns] = full
		}
	}

	// load in Brick
	if cfg.ReloadBrick {
		p := turtle.GetParser()
		relships, _ := p.Parse(cfg.BrickFrameTTL)
		classships, _ := p.Parse(cfg.BrickClassTTL)
		err = db.loadRelationships(relships)
		if err != nil {
			return nil, err
		}
		err = db.LoadDataset(relships)
		if err != nil {
			return nil, err
		}
		err = db.LoadDataset(classships)
		if err != nil {
			return nil, err
		}
		err = db.saveIndexes()
		if err != nil {
			return nil, err
		}
	}

	if cfg.ShowNamespaces {
		for k, v := range db.namespaces {
			log.Noticef("%s => %s", k, v)
		}
	}

	return db, nil
}

func (db *DB) Close() {
	checkError := func(err error) {
		if err != nil {
			log.Fatal(err)
		}
	}
	checkError(db.entityDB.Close())
	checkError(db.pkDB.Close())
	checkError(db.predDB.Close())
	checkError(db.graphDB.Close())
	checkError(db.extendedDB.Close())
}

// hashes the given URI into the byte array
func (db *DB) hashURI(u turtle.URI, dest []byte, salt uint32) {
	var hash uint32
	if len(dest) < 4 {
		dest = make([]byte, 4)
	}
	if salt > 0 {
		saltbytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(saltbytes, salt)
		hash = murmur.Murmur3(append(u.Bytes(), saltbytes...))
	} else {
		hash = murmur.Murmur3(u.Bytes())
	}
	binary.LittleEndian.PutUint32(dest, hash)
}

func (db *DB) insertEntity(entity turtle.URI, hashdest []byte) error {
	// check if we've inserted Subject already
	if exists, err := db.entityDB.Has(entity.Bytes(), nil); err == nil && exists {
		// populate hash anyway
		hash, err := db.entityDB.Get(entity.Bytes(), nil)
		copy(hashdest, hash[:])
		return err
	} else if err != nil {
		return errors.Wrapf(err, "Error checking db membership for %s", entity.String())
	}
	// generate the hash
	var salt = uint32(0)
	db.hashURI(entity, hashdest, salt)
	for {
		if exists, err := db.pkDB.Has(hashdest, nil); err == nil && exists {
			log.Warning("hash exists")
			salt += 1
			db.hashURI(entity, hashdest, salt)
		} else if err != nil {
			return errors.Wrapf(err, "Error checking db membership for %v", hashdest)
		} else {
			break
		}
	}

	// insert the hash into the entity and prefix dbs
	if err := db.entityDB.Put(entity.Bytes(), hashdest, nil); err != nil {
		return errors.Wrapf(err, "Error inserting entity %s", entity.String())
	}
	if err := db.pkDB.Put(hashdest, entity.Bytes(), nil); err != nil {
		return errors.Wrapf(err, "Error inserting pk %s", hashdest)
	}
	return nil
}

// for each part of the triple (subject, predicate, object), we check if its already in the entity database.
// If it is, we can skip it. If not, we generate a murmur3 hash for the entity, and then
// 0. check if we've already inserted the entity (skip if we already have)
// 1. check if the hash is unique (check membership in pk db) - if it isn't then we add a salt and check again
// 2. insert hash => []byte(entity) into pk db
// 3. insert []byte(entity) => hash into entity db
func (db *DB) insertEntityTx(entity turtle.URI, hashdest []byte, enttx, pktx *leveldb.Transaction) error {
	// check if we've inserted Subject already
	if exists, err := enttx.Has(entity.Bytes(), nil); err == nil && exists {
		// populate hash anyway
		hash, err := enttx.Get(entity.Bytes(), nil)
		copy(hashdest, hash[:])
		return err
	} else if err != nil {
		return errors.Wrapf(err, "Error checking db membership for %s", entity.String())
	}
	// generate the hash
	var salt = uint32(0)
	db.hashURI(entity, hashdest, salt)
	for {
		if exists, err := pktx.Has(hashdest, nil); err == nil && exists {
			log.Warning("hash exists")
			salt += 1
			db.hashURI(entity, hashdest, salt)
		} else if err != nil {
			return errors.Wrapf(err, "Error checking db membership for %v", hashdest)
		} else {
			break
		}
	}

	// insert the hash into the entity and prefix dbs
	if err := enttx.Put(entity.Bytes(), hashdest, nil); err != nil {
		return errors.Wrapf(err, "Error inserting entity %s", entity.String())
	}
	if err := pktx.Put(hashdest, entity.Bytes(), nil); err != nil {
		return errors.Wrapf(err, "Error inserting pk %s", hashdest)
	}
	return nil
}

func (db *DB) loadPredicateEntity(predicate turtle.URI, _predicateHash, _subjectHash, _objectHash []byte, predtx *leveldb.Transaction) error {
	var (
		pred          *PredicateEntity
		rpred         *PredicateEntity
		found         bool
		predicateHash Key
		subjectHash   Key
		objectHash    Key
	)
	predicateHash.FromSlice(_predicateHash)
	subjectHash.FromSlice(_subjectHash)
	objectHash.FromSlice(_objectHash)

	if pred, found = db.predIndex[predicate]; !found {
		pred = NewPredicateEntity()
		pred.PK = predicateHash
	}

	pred.AddSubjectObject(subjectHash, objectHash)
	db.predIndex[predicate] = pred

	if reverse, found := db.relationships[predicate]; found {
		if rpred, found = db.predIndex[reverse]; !found {
			rpred = NewPredicateEntity()
			rpred.PK = predicateHash
		}
		rpred.AddSubjectObject(objectHash, subjectHash)
		db.predIndex[reverse] = rpred
	}

	return nil
}

func (db *DB) saveIndexes() error {
	f, err := os.Create(db.path + "/predIndex")
	if err != nil {
		return err
	}

	pi := make(PredIndex)
	for uri, pe := range db.predIndex {
		pi[uri.String()] = pe
	}

	if err := msgp.Encode(f, pi); err != nil {
		return err
	}

	f, err = os.Create(db.path + "/relshipIndex")
	if err != nil {
		return err
	}

	ri := make(RelshipIndex)
	for uri, uri2 := range db.relationships {
		ri[uri.String()] = uri2.String()
	}

	if err := msgp.Encode(f, ri); err != nil {
		return err
	}

	f, err = os.Create(db.path + "/namespaceIndex")
	if err != nil {
		return err
	}
	if err := msgp.Encode(f, NamespaceIndex(db.namespaces)); err != nil {
		return err
	}

	return nil
}

func (db *DB) loadRelationships(dataset turtle.DataSet) error {
	// iterate through dataset, and pull out all that have a "rdf:type" of "owl:ObjectProperty"
	// then we want to find the mapping that has "owl:inverseOf"
	var relationships = make(map[turtle.URI]struct{})

	rdf_namespace, found := dataset.Namespaces["rdf"]
	if !found {
		return errors.New("Relationships has no rdf namespace")
	}
	owl_namespace, found := dataset.Namespaces["owl"]
	if !found {
		return errors.New("Relationships has no owl namespace")
	}

	for _, triple := range dataset.Triples {
		if triple.Predicate.Namespace == rdf_namespace &&
			triple.Predicate.Value == "type" &&
			triple.Object.Namespace == owl_namespace &&
			triple.Object.Value == "ObjectProperty" {
			relationships[triple.Subject] = struct{}{}
			db.transitiveEdges[triple.Subject] = struct{}{}
		}
	}

	for _, triple := range dataset.Triples {
		if triple.Predicate.Namespace == owl_namespace && triple.Predicate.Value == "inverseOf" {
			// check that the subject/object of the inverseOf relationships are both actually relationships
			if _, found := relationships[triple.Subject]; !found {
				continue
			}
			if _, found := relationships[triple.Object]; !found {
				continue
			}
			db.relationships[triple.Subject] = triple.Object
			db.relationships[triple.Object] = triple.Subject
			db.transitiveEdges[triple.Subject] = struct{}{}
			db.transitiveEdges[triple.Object] = struct{}{}
		}
		// check if a relationship is transitive
		//if triple.Predicate.Namespace == owl_namespace && triple.Predicate.Value == "a" &&
		//	triple.Object.Namespace == owl_namespace && triple.Object.Value == "TransitiveProperty" {
		//}
	}

	return nil
}

func (db *DB) LoadDataset(dataset turtle.DataSet) error {
	db.loading = true
	start := time.Now()
	// merge, don't set outright
	for abbr, full := range dataset.Namespaces {
		if abbr != "" {
			db.namespaces[abbr] = full
		}
	}
	// start transactions
	enttx, err := db.entityDB.OpenTransaction()
	if err != nil {
		return errors.Wrap(err, "Could not open transaction on entity dataset")
	}
	pktx, err := db.pkDB.OpenTransaction()
	if err != nil {
		return errors.Wrap(err, "Could not open transaction on pk dataset")
	}
	predtx, err := db.predDB.OpenTransaction()
	if err != nil {
		return errors.Wrap(err, "Could not open transaction on pred dataset")
	}
	// load triples and primary keys
	var (
		subjectHash   = make([]byte, 4)
		predicateHash = make([]byte, 4)
		objectHash    = make([]byte, 4)
	)
	b := db.textidx.NewBatch()
	for _, triple := range dataset.Triples {
		// add classes to the text idx
		if triple.Predicate.String() == "http://www.w3.org/1999/02/22-rdf-syntax-ns#type" && triple.Object.String() == "http://www.w3.org/2002/07/owl#Class" {
			sub := strings.Replace(triple.Subject.String(), "_", " ", -1)
			if err := b.Index(triple.Subject.String(), sub); err != nil && len(triple.Subject.String()) > 0 {
				return errors.Wrapf(err, "Could not add subject %s to text index (%s)", triple.Subject, triple)
			}
			//			pred := strings.Replace(triple.Predicate.String(), "_", " ", -1)
			//			if err := b.Index(triple.Predicate.String(), pred); err != nil && len(triple.Predicate.String()) > 0 {
			//				return errors.Wrapf(err, "Could not add predicate %s to text index (%s)", triple.Predicate, triple)
			//			}
			//			obj := strings.Replace(triple.Object.String(), "_", " ", -1)
			//			if err := b.Index(triple.Object.String(), obj); err != nil && len(triple.Object.String()) > 0 {
			//				return errors.Wrapf(err, "Could not add object %s to text index (%s)", triple.Object, triple)
			//			}
		}

		if err := db.insertEntityTx(triple.Subject, subjectHash, enttx, pktx); err != nil {
			return err
		}
		if err := db.insertEntityTx(db.relationships[triple.Predicate], predicateHash, enttx, pktx); err != nil {
			return err
		}
		if err := db.insertEntityTx(triple.Predicate, predicateHash, enttx, pktx); err != nil {
			return err
		}
		if err := db.insertEntityTx(triple.Object, objectHash, enttx, pktx); err != nil {
			return err
		}
		if err := db.loadPredicateEntity(triple.Predicate, predicateHash, subjectHash, objectHash, predtx); err != nil {
			return err
		}
	}

	// batch the text index update
	if err := db.textidx.Batch(b); err != nil {
		return errors.Wrap(err, "Could not save batch text index")
	}

	for pred, _ := range db.relationships {
		if err := db.insertEntityTx(pred, predicateHash, enttx, pktx); err != nil {
			return err
		}
		pred.Value += "+"
		if err := db.insertEntityTx(pred, predicateHash, enttx, pktx); err != nil {
			return err
		}
	}

	// finish those transactions
	if err := enttx.Commit(); err != nil {
		return errors.Wrap(err, "Could not commit transaction")
	}
	if err := pktx.Commit(); err != nil {
		return errors.Wrap(err, "Could not commit transaction")
	}
	if err := predtx.Commit(); err != nil {
		return errors.Wrap(err, "Could not commit transaction")
	}
	log.Infof("Built lookup tables in %s", time.Since(start))

	start = time.Now()
	if err := db.buildGraph(dataset); err != nil {
		return errors.Wrap(err, "Could not build graph")
	}
	log.Infof("Built graph in %s", time.Since(start))

	for pfx, uri := range db.namespaces {
		fmt.Printf("%s => %s\n", pfx, uri)
	}
	// save indexes after loading database
	err = db.saveIndexes()
	if err != nil {
		return err
	}
	db.loading = false
	return nil
}

// returns the uint32 hash of the given URI (this is adjusted for uniqueness)
func (db *DB) GetHash(entity turtle.URI) (Key, error) {
	var rethash Key
	if hash, err := db.entityHashCache.Get(entity.Bytes()); err != nil {
		if err == freecache.ErrNotFound {
			val, err := db.entityDB.Get(entity.Bytes(), nil)
			if err != nil {
				return emptyKey, errors.Wrapf(err, "Could not get Entity for %s", entity)
			}
			copy(rethash[:], val)
			if rethash == emptyKey {
				return emptyKey, errors.New("Got bad hash")
			}
			db.entityHashCache.Set(entity.Bytes(), rethash[:], -1) // no expiry
			return rethash, nil
		} else {
			return emptyKey, errors.Wrapf(err, "Could not get Entity for %s", entity)
		}
	} else {
		copy(rethash[:], hash)
	}
	return rethash, nil
}

func (db *DB) MustGetHash(entity turtle.URI) Key {
	val, err := db.GetHash(entity)
	if err != nil {
		log.Error(errors.Wrapf(err, "Could not get hash for %s", entity))
		return emptyKey
	}
	return val
}

func (db *DB) GetURI(hash Key) (turtle.URI, error) {
	db.uriLock.RLock()
	if uri, found := db.uriCache[hash]; found {
		db.uriLock.RUnlock()
		return uri, nil
	}
	db.uriLock.RUnlock()
	db.uriLock.Lock()
	defer db.uriLock.Unlock()
	val, err := db.pkDB.Get(hash[:], nil)
	if err != nil {
		return turtle.URI{}, err
	}
	uri := turtle.ParseURI(string(val))
	db.uriCache[hash] = uri
	return uri, nil
}

func (db *DB) MustGetURI(hash Key) turtle.URI {
	if hash == emptyKey {
		return turtle.URI{}
	}
	uri, err := db.GetURI(hash)
	if err != nil {
		log.Error(errors.Wrapf(err, "Could not get URI for %v", hash))
		return turtle.URI{}
	}
	return uri
}

func (db *DB) MustGetURIStringHash(hash string) turtle.URI {
	var c Key
	copy(c[:], []byte(hash))
	return db.MustGetURI(c)
}

func (db *DB) GetEntity(uri turtle.URI) (*Entity, error) {
	hash, err := db.GetHash(uri)
	if err != nil {
		return nil, err
	}
	return db.GetEntityFromHash(hash)
}

func (db *DB) GetEntityFromHash(hash Key) (*Entity, error) {
	db.eocLock.RLock()
	if ent, found := db.entityObjectCache[hash]; found {
		db.eocLock.RUnlock()
		return ent, nil
	}
	db.eocLock.RUnlock()
	db.eocLock.Lock()
	defer db.eocLock.Unlock()
	bytes, err := db.graphDB.Get(hash[:], nil)
	if err != nil {
		return nil, errors.Wrapf(err, "Could not get Entity from graph for %s", db.MustGetURI(hash))
	}
	ent := NewEntity()
	_, err = ent.UnmarshalMsg(bytes)
	db.entityObjectCache[hash] = ent
	return ent, err
}

func (db *DB) MustGetEntityFromHash(hash Key) *Entity {
	e, err := db.GetEntityFromHash(hash)
	if err != nil {
		log.Error(errors.Wrap(err, "Could not get entity"))
		return nil
	}
	return e
}

func (db *DB) MustGetEntityIndexFromHash(hash Key) *EntityExtendedIndex {
	e, err := db.GetEntityIndexFromHash(hash)
	if err != nil {
		log.Error(errors.Wrap(err, "Could not get entity index"))
		return nil
	}
	return e
}

func (db *DB) DumpEntity(ent *Entity) {
	fmt.Println("DUMPING", db.MustGetURI(ent.PK))
	for edge, list := range ent.OutEdges {
		fmt.Printf(" OUT: %s \n", db.MustGetURIStringHash(edge).Value)
		for _, l := range list {
			fmt.Printf("     -> %s\n", db.MustGetURI(l).Value)
		}
	}
	for edge, list := range ent.InEdges {
		fmt.Printf(" In: %s \n", db.MustGetURIStringHash(edge).Value)
		for _, l := range list {
			fmt.Printf("     <- %s\n", db.MustGetURI(l).Value)
		}
	}
}

func (db *DB) GetEntityTx(graphtx *leveldb.Transaction, uri turtle.URI) (*Entity, error) {
	var entity = NewEntity()
	hash, err := db.GetHash(uri)
	if err != nil {
		return nil, err
	}
	bytes, err := graphtx.Get(hash[:], nil)
	if err != nil {
		return nil, err
	}
	_, err = entity.UnmarshalMsg(bytes)
	if err != nil {
		return nil, err
	}
	return entity, nil
}

func (db *DB) GetEntityFromHashTx(graphtx *leveldb.Transaction, hash Key) (*Entity, error) {
	bytes, err := graphtx.Get(hash[:], nil)
	if err != nil {
		return nil, err
	}
	ent := NewEntity()
	_, err = ent.UnmarshalMsg(bytes)
	return ent, err
}

func (db *DB) GetEntityIndexFromHashTx(extendtx *leveldb.Transaction, hash Key) (*EntityExtendedIndex, error) {
	bytes, err := extendtx.Get(hash[:], nil)
	if err != nil {
		return nil, err
	}
	ent := NewEntityExtendedIndex()
	_, err = ent.UnmarshalMsg(bytes)
	return ent, err
}

func (db *DB) GetEntityIndexFromHash(hash Key) (*EntityExtendedIndex, error) {
	db.eicLock.RLock()
	if ent, found := db.entityIndexCache[hash]; found {
		db.eicLock.RUnlock()
		return ent, nil
	}
	db.eicLock.RUnlock()
	db.eicLock.Lock()
	defer db.eicLock.Unlock()
	bytes, err := db.extendedDB.Get(hash[:], nil)
	if err != nil && err != leveldb.ErrNotFound {
		return nil, errors.Wrapf(err, "Could not get EntityIndex from graph for %s", db.MustGetURI(hash))
	} else if err == leveldb.ErrNotFound {
		db.entityIndexCache[hash] = nil
		return nil, nil
	}
	ent := NewEntityExtendedIndex()
	_, err = ent.UnmarshalMsg(bytes)
	db.entityIndexCache[hash] = ent
	return ent, err
}

func (db *DB) expandFilter(filter query.Filter) query.Filter {
	if !strings.HasPrefix(filter.Subject.Value, "?") {
		if full, found := db.namespaces[filter.Subject.Namespace]; found {
			filter.Subject.Namespace = full
		}
	}
	if !strings.HasPrefix(filter.Object.Value, "?") {
		if full, found := db.namespaces[filter.Object.Namespace]; found {
			filter.Object.Namespace = full
		}
	}
	for idx2, pred := range filter.Path {
		if !strings.HasPrefix(pred.Predicate.Value, "?") {
			if full, found := db.namespaces[pred.Predicate.Namespace]; found {
				pred.Predicate.Namespace = full
			}
			filter.Path[idx2] = pred
		}
	}
	return filter
}

func (db *DB) expandOrClauseFilters(orc query.OrClause) query.OrClause {
	for fidx, filter := range orc.Terms {
		orc.Terms[fidx] = db.expandFilter(filter)
	}
	for fidx, filter := range orc.LeftTerms {
		orc.LeftTerms[fidx] = db.expandFilter(filter)
	}
	for fidx, filter := range orc.RightTerms {
		orc.RightTerms[fidx] = db.expandFilter(filter)
	}
	for oidx, oc := range orc.LeftOr {
		orc.LeftOr[oidx] = db.expandOrClauseFilters(oc)
	}
	for oidx, oc := range orc.RightOr {
		orc.RightOr[oidx] = db.expandOrClauseFilters(oc)
	}
	return orc
}
