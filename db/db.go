package db

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/gtfierro/hod/config"
	"github.com/gtfierro/hod/turtle"

	"github.com/blevesearch/bleve"
	"github.com/coocood/freecache"
	"github.com/op/go-logging"
	"github.com/pkg/errors"
	logrus "github.com/sirupsen/logrus"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/filter"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/tinylib/msgp/msgp"
)

// logger
var log *logging.Logger
var emptyKey = Key{}

func init() {
	log = logging.MustGetLogger("hod")
	var format = "%{color}%{level} %{shortfile} %{time:Jan 02 15:04:05} %{color:reset} ▶ %{message}"
	var logBackend = logging.NewLogBackend(os.Stderr, "", 0)
	logBackendLeveled := logging.AddModuleLevel(logBackend)
	logging.SetBackend(logBackendLeveled)
	logging.SetFormatter(logging.MustStringFormatter(format))

	logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true, ForceColors: true})
	logrus.SetOutput(os.Stdout)
	logrus.SetLevel(logrus.InfoLevel)
}

// TODO: evict hash when writes happen
type DB struct {
	path string
	name string
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
	relLock       sync.RWMutex
	// stores which edges can be 'rolled forward' in the index
	transitiveEdges map[turtle.URI]struct{}
	// store the namespace prefixes as strings
	namespaces map[string]string
	// cache for entity hashes
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

func newDB(name string, cfg *config.Config) (*DB, error) {
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
		name:                   name,
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
	if cfg.ReloadOntologies {
		p := turtle.GetParser()
		for _, ontologyFile := range cfg.Ontologies {
			ds, _ := p.Parse(ontologyFile)

			// TODO: testing
			tx, err := db.openTransaction()
			if err != nil {
				tx.discard()
				panic(err)
			}
			if err := tx.addTriples(ds); err != nil {
				tx.discard()
				panic(err)
			}

			if err := tx.commit(); err != nil {
				tx.discard()
				return nil, err
			}

			for abbr, full := range ds.Namespaces {
				if abbr != "" {
					db.namespaces[abbr] = full
				}
			}

		}
		err = db.saveIndexes()
		if err != nil {
			return nil, err
		}
	}

	//if cfg.ReloadOntologies {
	//	p := turtle.GetParser()
	//	for _, ontologyFile := range cfg.Ontologies {
	//		ds, _ := p.Parse(ontologyFile)
	//		err = db.loadDataset(ds)

	//		if err != nil {
	//			return nil, err
	//		}
	//	}
	//	err = db.saveIndexes()
	//	if err != nil {
	//		return nil, err
	//	}
	//}

	if cfg.ShowNamespaces {
		var dmp strings.Builder
		lenK := len("Prefix")
		lenV := len("Namespace")

		for k, v := range db.namespaces {
			if len(k) > lenK {
				lenK = len(k)
			}
			if len(v) > lenV {
				lenV = len(v)
			}
		}
		fmt.Fprintf(&dmp, "+ %s +\n", strings.Repeat("-", lenK+lenV+3))
		fmt.Fprintf(&dmp, "| Prefix%s | Namespace%s |\n", strings.Repeat(" ", lenK-len("Prefix")), strings.Repeat(" ", lenV-len("Namespace")))
		fmt.Fprintf(&dmp, "+ %s +\n", strings.Repeat("-", lenK+lenV+3))
		for k, v := range db.namespaces {
			kpad := strings.Repeat(" ", lenK-len(k))
			vpad := strings.Repeat(" ", lenV-len(v))
			fmt.Fprintf(&dmp, "| %s%s | %s%s |\n", k, kpad, v, vpad)
		}
		fmt.Fprintf(&dmp, "+ %s +\n", strings.Repeat("-", lenK+lenV+3))
		fmt.Println(dmp.String())

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
	checkError(db.textidx.Close())
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
	var salt = uint64(0)
	hashURI(entity, hashdest, salt)
	for {
		if exists, err := db.pkDB.Has(hashdest, nil); err == nil && exists {
			log.Warning("hash exists")
			salt += 1
			hashURI(entity, hashdest, salt)
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
	var salt = uint64(0)
	hashURI(entity, hashdest, salt)
	for {
		if exists, err := pktx.Has(hashdest, nil); err == nil && exists {
			log.Warning("hash exists")
			salt += 1
			hashURI(entity, hashdest, salt)
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

func (db *DB) loadPredicateEntity(predicate turtle.URI, _predicateHash, _subjectHash, _objectHash []byte) error {
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
