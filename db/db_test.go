package db

import (
	"io"
	"strings"
	"testing"

	"github.com/gtfierro/hod/config"
	turtle "github.com/gtfierro/hod/goraptor"
	"github.com/gtfierro/hod/query"
)

func TestDBQuery(t *testing.T) {
	cfg, err := config.ReadConfig("testhodconfig.yaml")
	if err != nil {
		t.Error(err)
		return
	}
	cfg.DBPath = "test_databases/testdb"
	db, err := NewDB(cfg)
	if err != nil {
		t.Error(err)
		return
	}
	for _, test := range []struct {
		query   string
		results []ResultMap
	}{
		{
			"SELECT ?x WHERE { ?x rdf:type brick:Room . };",
			[]ResultMap{{"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#room_1")}},
		},
		{
			"SELECT ?x WHERE { ?ahu rdf:type brick:AHU . ?ahu bf:feeds ?x .};",
			[]ResultMap{{"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#vav_1")}},
		},
		{
			"SELECT ?x WHERE { ?ahu rdf:type brick:AHU . ?ahu bf:feeds+ ?x .};",
			[]ResultMap{{"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#hvaczone_1")}, {"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#vav_1")}},
		},
		{
			"SELECT ?x WHERE { ?ahu rdf:type brick:AHU . ?x bf:isFedBy+ ?ahu .};",
			[]ResultMap{{"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#hvaczone_1")}, {"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#vav_1")}},
		},
		{
			"SELECT ?x WHERE { ?ahu rdf:type brick:AHU . ?ahu bf:feeds/bf:feeds ?x .};",
			[]ResultMap{{"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#hvaczone_1")}},
		},
		{
			"SELECT ?x WHERE { ?ahu rdf:type brick:AHU . ?ahu bf:feeds/bf:feeds+ ?x .};",
			[]ResultMap{{"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#hvaczone_1")}},
		},
		{
			"SELECT ?x WHERE { ?ahu rdf:type brick:AHU . ?ahu bf:feeds/bf:feeds? ?x .};",
			[]ResultMap{{"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#hvaczone_1")}, {"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#vav_1")}},
		},
		{
			"SELECT ?x WHERE { ?ahu rdf:type brick:AHU . ?x bf:isFedBy/bf:isFedBy? ?ahu .};",
			[]ResultMap{{"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#hvaczone_1")}, {"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#vav_1")}},
		},
		{
			"SELECT ?x WHERE { ?ahu rdf:type brick:AHU . ?ahu bf:feeds* ?x .};",
			[]ResultMap{{"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#hvaczone_1")}, {"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#vav_1")}, {"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#vav_1")}},
		},
		{
			"SELECT ?x WHERE { ?ahu rdf:type brick:AHU . ?x bf:isFedBy* ?ahu .};",
			[]ResultMap{{"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#hvaczone_1")}, {"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#vav_1")}, {"?x": turtle.ParseURI("http://buildsys.org/ontologies/building_example#vav_1")}},
		},
		{
			"SELECT ?vav ?room WHERE { ?vav rdf:type brick:VAV . ?room rdf:type brick:Room . ?zone rdf:type brick:HVAC_Zone . ?vav bf:feeds+ ?zone . ?room bf:isPartOf ?zone . }; ",
			[]ResultMap{{"?room": turtle.ParseURI("http://buildsys.org/ontologies/building_example#room_1"), "?vav": turtle.ParseURI("http://buildsys.org/ontologies/building_example#vav_1")}},
		},
		{
			"SELECT ?sensor WHERE { ?sensor rdf:type/rdfs:subClassOf* brick:Zone_Temperature_Sensor . };",
			[]ResultMap{{"?sensor": turtle.ParseURI("http://buildsys.org/ontologies/building_example#ztemp_1")}},
		},
	} {
		q, e := query.Parse(strings.NewReader(test.query))
		if e != nil {
			t.Error(test.query, e)
			continue
		}
		result := db.RunQuery(q)
		if !compareResultMapList(test.results, result.Rows) {
			t.Errorf("Results for %s had\n %+v\nexpected\n %+v", test.query, result.Rows, test.results)
			return
		}
	}
}

func BenchmarkQueryPerformance1(b *testing.B) {
	cfg, err := config.ReadConfig("testhodconfig.yaml")
	if err != nil {
		b.Error(err)
		return
	}
	cfg.DBPath = "test_databases/berkeleytestdb"
	db, err := NewDB(cfg)
	if err != nil {
		b.Error(err)
		return
	}
	benchmarks := []struct {
		name  string
		query io.Reader
	}{
		{"SimpleSubjectVarTriple", strings.NewReader("SELECT ?x WHERE { ?x rdf:type brick:Room . };")},
		{"LongerQuery1", strings.NewReader("SELECT ?vav ?room WHERE { ?vav rdf:type brick:VAV . ?room rdf:type brick:Room . ?zone rdf:type brick:HVAC_Zone . ?vav bf:feeds+ ?zone . ?room bf:isPartOf ?zone . }; ")},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				q, _ := query.Parse(bm.query)
				db.RunQuery(q)
			}
		})
	}
}
