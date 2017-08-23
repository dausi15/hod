from rdflib import Graph, Namespace, URIRef, Literal
import rdflib
import re
RDF = Namespace('http://www.w3.org/1999/02/22-rdf-syntax-ns#')
RDFS = Namespace('http://www.w3.org/2000/01/rdf-schema#')
BRICK = Namespace('https://brickschema.org/schema/1.0.1/Brick#')
BRICKFRAME = Namespace('https://brickschema.org/schema/1.0.1/BrickFrame#')
g = rdflib.Graph()
g.bind( 'rdf', RDF)
g.bind( 'rdfs', RDFS)
g.bind( 'brick', BRICK)
g.bind( 'bf', BRICKFRAME)
EX = Namespace('http://buildsys.org/ontologies/building_example#')
g.bind('bldg', EX)
g.add((EX.floor_1, RDF.type, BRICK.Floor))
g.add((EX.room_1, RDF.type, BRICK.Room))
g.add((EX.room_1, RDFS.label, Literal("Room 1")))
g.add((EX.vav_1, RDF.type, BRICK.VAV))
g.add((EX.hvaczone_1, RDF.type, BRICK.HVAC_Zone))
g.add((EX.ahu_1, RDF.type, BRICK.AHU))
g.add((EX.ztemp_1, RDF.type, BRICK.Zone_Temperature_Sensor))

g.add((EX.ztemp_1, BRICKFRAME.isPointOf, EX.vav_1))

g.add((EX.ahu_1, BRICKFRAME.feeds, EX.vav_1))
g.add((EX.vav_1, BRICKFRAME.feeds, EX.hvaczone_1))

g.add((EX.room_1, BRICKFRAME.isPartOf, EX.hvaczone_1))
g.add((EX.room_1, BRICKFRAME.isPartOf, EX.floor_1))

g.serialize(destination='example.ttl',format='turtle')
print len(g)
