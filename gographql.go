package gographql

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type targetType int

const (
	graphqlObject targetType = iota
	graphqlInputObject
)

func (tt targetType) String() (name string) {
	switch tt {
	case graphqlObject:
		name = "graphqlObject"
	case graphqlInputObject:
		name = "graphqlInputObject"
	}
	return
}

var (
	REstub             = regexp.MustCompile(`(.*)Stub`)
	RElist             = regexp.MustCompile(`\[(.*)Stub\]`)
	RENonNull          = regexp.MustCompile(`(.*)Stub\!`)
	REreturnsPtr       = regexp.MustCompile(`\(\) \*`)
	objectMapper       = newTypeMapper()
	SubstitutedTypeKey = "replaceTypeWith"
	indentBuf          [10000]byte
	log                = logrus.New()
)

func init() {
	for i := 0; i < len(indentBuf); i++ {
		indentBuf[i] = ' '
	}
}

type input struct {
	object  *graphql.InputObject
	html    *strings.Builder
	dataMap interface{}
}

type typeMapper struct {
	graphqlTypes        map[string]graphql.Type
	parentTypes         map[string]bool
	level               uint
	methods             map[string]string
	indexValues         string
	sliceDepth          uint
	typeReplacer        TypeReplacer
	fieldResolverFinder FieldResolverFinder
	targetType          targetType
}

func newTypeMapper() (tm typeMapper) {
	tm = typeMapper{
		graphqlTypes:        map[string]graphql.Type{},
		parentTypes:         map[string]bool{},
		level:               0,
		methods:             map[string]string{},
		indexValues:         "abcdefghijklmnopqrstuvwzyz",
		typeReplacer:        defaultTypeReplacer{},
		fieldResolverFinder: defaultFieldResolverFinder{},
	}
	return tm
}

// FieldResolverFinder returns the graphql resolver function for the given field Type.
// Returns nil if not reflect.Type is found for typeName.
type FieldResolverFinder interface {
	GetResolver(fieldTypeName, substituteTypeName string) graphql.FieldResolveFn
}

type defaultFieldResolverFinder struct {
}

func (dfrf defaultFieldResolverFinder) GetResolver(fieldTypeName, substituteTypeName string) graphql.FieldResolveFn {
	return nil
}

// SetFieldResolverFinder sets the finder to use.
func SetFieldResolverFinder(finder FieldResolverFinder) {
	objectMapper.SetFieldResolverFinder(finder)
}

// SetFieldResolverFinder sets the finder to use.
func (tm *typeMapper) SetFieldResolverFinder(finder FieldResolverFinder) {
	tm.fieldResolverFinder = finder
}

// TypeReplacer returns a reflect.Type value for the given typeName.
// Returns nil if no reflect.Type is found for typeName.
type TypeReplacer interface {
	GetType(typeName string) *reflect.Type
}

type defaultTypeReplacer struct {
}

func (dtf defaultTypeReplacer) GetType(typeName string) *reflect.Type {
	return nil
}

// SetTypeReplacer sets the replacer to use.
func SetTypeReplacer(typeFinder TypeReplacer) {
	objectMapper.SetTypeReplacer(typeFinder)
}

// SetTypeReplacer sets the replacer to use.
func (tm *typeMapper) SetTypeReplacer(typeFinder TypeReplacer) {
	tm.typeReplacer = typeFinder
}

func (tm *typeMapper) indent() string {
	return string(indentBuf[0 : 3*tm.level])
}

// SetDescription sets the the description for the named field of for the type.
func SetDescription(graphqlType interface{}, fieldName, description string) {
	switch object := graphqlType.(type) {
	case *graphql.Object:
		if _, ok := object.Fields()[fieldName]; ok {
			object.Fields()[fieldName].Description = description
		}
	case *graphql.InputObject:
		if _, ok := object.Fields()[fieldName]; ok {
			object.Fields()[fieldName].PrivateDescription = description
		}
	default:
		log.Errorf("do not know about type %T", object)
	}
}

// Object marshals a Go structure to a graphQL Object (for output).
// If the structure has already been marshalled, the one that was found is returned.
func GoToGraphqlObject(goStruct interface{}) (object *graphql.Object, err error) {
	return objectMapper.GoToGraphqlObject(goStruct)
}

// Object marshals a Go structure to a graphQL Object (for output).
// If the structure has already been marshalled, the one that was found is returned.
func (tm *typeMapper) GoToGraphqlObject(goStruct interface{}) (object *graphql.Object, err error) {
	tm.targetType = graphqlObject
	graphqlType, err := tm.goToGraphqlType(goStruct)
	if err != nil {
		return
	}
	if nil == graphqlType {
		err = errors.New("got nil")
		log.Error(err)
		return
	}
	object, ok := graphqlType.(*graphql.Object)
	if !ok {
		err = fmt.Errorf("object got type %T; expected type graphql.Object", graphqlType)
		log.Error(err)
		return
	}
	return
}

// InputObject marshals a Go structure to a graphQL InputObject.
// If the structure has already been marshalled, the one that was found is returned.
func GoToGraphqlInputObject(goStruct interface{}) (inputObject *graphql.InputObject, err error) {
	return objectMapper.GoToGraphqlInputObject(goStruct)
}

// InputObject marshals a Go structure to a graphQL InputObject.
// If the structure has already been marshalled, the one that was found is returned.
func (tm *typeMapper) GoToGraphqlInputObject(goStruct interface{}) (inputObject *graphql.InputObject, err error) {
	tm.targetType = graphqlInputObject
	graphqlType, err := tm.goToGraphqlType(goStruct)
	if err != nil {
		return
	}
	if nil == graphqlType {
		err = errors.New("got nil")
		log.Error(err)
		return
	}
	inputObject, ok := graphqlType.(*graphql.InputObject)
	if !ok {
		err = fmt.Errorf("object got type %T; expected type graphql.InputObject", graphqlType)
		log.Error(err)
		return
	}
	return
}

// goToGraphqlType marshals a Go structure to a graphQL Type.
// If the structure has already been marshalled, the one that was found is returned.
func (tm *typeMapper) goToGraphqlType(goStruct interface{}) (graphqlType graphql.Type, err error) {
	flagLogLevel := viper.GetString("goGraphqlLogLevel")
	if theLogrusConstant, err := logrus.ParseLevel(flagLogLevel); nil == err {
		if nil != err {
			log.Errorf("%v%v", tm.indent(), err)
		} else {
			thePriorLogrusConstant := log.GetLevel()
			log.SetLevel(theLogrusConstant)
			defer log.SetLevel(thePriorLogrusConstant)
		}
	}
	structure, ok := goStruct.(reflect.Type)
	if !ok {
		structure = reflect.TypeOf(goStruct)
		if nil == structure {
			err = errors.New("the input argument cannot be nil.")
			return
		}
	}
	if reflect.Ptr == structure.Kind() {
		structure = structure.Elem()
	}
	if reflect.Struct != structure.Kind() {
		err = errors.New("the input argument is not a reflect.Struct Kind.")
		return
	}
	structureName := structure.Name()
	if "" == structureName {
		err = errors.New("Names of graphql types cannot be a zero-length string; skipping - won't marshall this one.")
		return
	}
	var fields interface{}
	if tm.targetType == graphqlInputObject {
		fields = graphql.InputObjectConfigFieldMap{}
		structureName = structureName + "_Input"
	}
	graphqlType, defined := tm.graphqlTypes[structureName]
	if defined {
		log.Infof(`%vType "%v" has already been defined and so am returning that one.`, tm.indent(), structureName)
		return
	}
	if tm.targetType == graphqlObject {
		fields = graphql.Fields{}
	}
	if _, exists := tm.parentTypes[structureName]; exists { // this Type is a child of itself
		log.Infof(
			`%vStruct "%v" is nested in itself and so am inserting a stub/reference for resolution later.`,
			tm.indent(), structureName,
		)
		typeName := structureName + "Stub"
		// every object must have at least one field.
		fieldName := "aField"
		switch fields := fields.(type) {
		case graphql.Fields:
			log.Infof("returning type %v", typeName)
			fields[fieldName] = &graphql.Field{
				Name: fieldName,
				Type: graphql.Int,
			}
			graphqlType = graphql.NewObject(
				graphql.ObjectConfig{
					Name:   typeName,
					Fields: fields,
				},
			)
			tm.graphqlTypes[structureName] = graphqlType
		case graphql.InputObjectConfigFieldMap:
			log.Infof("returning type %v", typeName)
			fields[fieldName] = &graphql.InputObjectFieldConfig{
				Type:         graphql.Int,
				DefaultValue: nil,
			}
			graphqlType = graphql.NewInputObject(
				graphql.InputObjectConfig{
					Name:   typeName,
					Fields: fields,
				},
			)
			tm.graphqlTypes[structureName] = graphqlType
		}
		return
	}
	tm.parentTypes[structureName] = true // indicates that this Type is in this marshalling process.
	tm.level++
	// func
	//   * replaces stubs with the actual definition
	//   * releases memory
	defer func() {
		delete(tm.parentTypes, structureName)
		tm.level--
		if 0 == tm.level {
			for _, Type := range tm.graphqlTypes {
				switch obj := Type.(type) {
				case *graphql.Object:
					for fieldKey, fieldDef := range obj.Fields() {
						stubbedTypeName := fieldDef.Type.Name()
						words := REstub.FindStringSubmatch(stubbedTypeName)
						if nil == words {
							continue
						}
						if _, exists := obj.Fields()[fieldKey]; exists {
							log.Printf("did delete %v.%v of type %v", obj.Name(), fieldKey, stubbedTypeName)
						}
						delete(obj.Fields(), fieldKey)
						typeName := words[1]
						fieldType, exists := tm.graphqlTypes[typeName]
						if !exists {
							log.Errorf(`%v %v object not found for typeName "%v"`, tm.indent(), tm.level, typeName)
							continue
						}
						kindName := fmt.Sprintf("%v", reflect.ValueOf(fieldDef.Type))
						if words := RElist.FindStringSubmatch(kindName); nil != words {
							typeName = words[1]
							fieldType = graphql.NewList(fieldType)
						}
						if words := RENonNull.FindStringSubmatch(kindName); nil != words {
							typeName = words[1]
							fieldType = graphql.NewNonNull(fieldType)
						}
						obj.AddFieldConfig(
							fieldKey,
							&graphql.Field{
								Name:              fieldKey,
								Type:              fieldType,
								Args:              graphql.FieldConfigArgument{},
								Resolve:           fieldDef.Resolve,
								DeprecationReason: fieldDef.DeprecationReason,
								Description:       fieldDef.Description,
							},
						)
						log.Infof(
							`%v %v Replaced %v.%v, of type %v with type %v.`,
							tm.indent(), tm.level, obj.Name(), fieldKey, stubbedTypeName, fieldType.Name(),
						)
					}
				case *graphql.InputObject:
					for fieldKey, fieldDef := range obj.Fields() {
						stubbedTypeName := fieldDef.Type.Name()
						words := REstub.FindStringSubmatch(stubbedTypeName)
						if nil == words {
							continue
						}
						if _, exists := obj.Fields()[fieldKey]; exists {
							log.Printf("did delete %v.%v of type %v", obj.Name(), fieldKey, stubbedTypeName)
						}
						delete(obj.Fields(), fieldKey)
						typeName := words[1]
						fieldType, exists := tm.graphqlTypes[typeName]
						if !exists {
							log.Errorf(`%v %v object not found for typeName "%v"`, tm.indent(), tm.level, typeName)
							continue
						}
						kindName := fmt.Sprintf("%v", reflect.ValueOf(fieldDef.Type))
						if words := RElist.FindStringSubmatch(kindName); nil != words {
							typeName = words[1]
							fieldType = graphql.NewList(fieldType)
						}
						if words := RENonNull.FindStringSubmatch(kindName); nil != words {
							typeName = words[1]
							fieldType = graphql.NewNonNull(fieldType)
						}
						obj.AddFieldConfig(
							fieldKey,
							&graphql.InputObjectFieldConfig{
								Type:         fieldType,
								DefaultValue: fieldDef.DefaultValue,
								Description:  fieldDef.Description(),
							},
						)
						log.Infof(
							`%v %v Replaced %v.%v, of type %v with type %v.`,
							tm.indent(), tm.level, obj.Name(), fieldKey, stubbedTypeName, fieldType.Name(),
						)
					}
				}
			}
			tm.parentTypes = map[string]bool{}
		}
	}()

	numFields := structure.NumField()
	log.Infof(`%vbegin reflecting on "%v"; it has %v fields`, tm.indent(), structureName, numFields)
	numFieldsMarshalled := 0
	for fieldNumber := 0; fieldNumber < numFields; fieldNumber++ {
		structField := structure.Field(fieldNumber)
		log.Infof("%v %v %v %v.%v", tm.indent(), tm.level, fieldNumber, structureName, structField.Name)
		graphqlFieldType, err := tm.goFieldToGraphql(structField, structureName)
		if nil != err {
			log.Infof(`"%v"Ignoring "%v.%v"; reason; %v`, tm.indent(), structureName, structField.Name, err)
			err = nil
			continue
		}
		fieldType := ""
		if words := strings.Split(structField.Type.String(), "."); len(words) > 1 {
			fieldType = words[1]
		}
		if required := structField.Tag.Get("required"); "true" == required && structField.Type.Kind() == reflect.Ptr {
			graphqlFieldType = graphql.NewNonNull(graphqlFieldType)
		}
		substituteTypeName, _ := structField.Tag.Lookup(SubstitutedTypeKey)

		description := structField.Tag.Get("description")
		switch fields := fields.(type) {
		case graphql.Fields:
			fields[structField.Name] = &graphql.Field{
				Name:        structField.Name,
				Type:        graphqlFieldType,
				Description: description,
				Resolve:     tm.fieldResolverFinder.GetResolver(fieldType, substituteTypeName),
			}
			numFieldsMarshalled = len(fields)
		case graphql.InputObjectConfigFieldMap:
			fields[structField.Name] = &graphql.InputObjectFieldConfig{
				Type:         graphqlFieldType,
				DefaultValue: nil,
				Description:  description,
			}
			numFieldsMarshalled = len(fields)
		}
	}
	log.Info(tm.indent(), "end reflecting on ", structureName)
	if 0 == numFieldsMarshalled {
		err = fmt.Errorf(`struct named "%v" has 0 marshalable fields; nothing to reflect on; skipping it`, structureName)
		return
	}
	switch fields := fields.(type) {
	case graphql.Fields:
		graphqlType = graphql.NewObject(graphql.ObjectConfig{Name: structureName, Fields: fields})
	case graphql.InputObjectConfigFieldMap:
		graphqlType = graphql.NewInputObject(graphql.InputObjectConfig{Name: structureName, Fields: fields})
	}
	tm.graphqlTypes[structureName] = graphqlType
	return
}

func (tm typeMapper) goFieldToGraphql(structField reflect.StructField, structName string) (output graphql.Output, err error) {
	structFieldType := structField.Type
	if structFieldType.Kind() == reflect.Ptr {
		structFieldType = structFieldType.Elem()
	}

	var substitutedType *reflect.Type
	substituteTypeName, ok := structField.Tag.Lookup(SubstitutedTypeKey)
	if ok {
		substitutedType = tm.typeReplacer.GetType(substituteTypeName)
	}
	if nil != substitutedType {
		log.Infof(
			`%vIn struct named "%v", substituting type "%v" of field named "%v" with type "%v"`,
			tm.indent(), structName, structFieldType.Name(), structField.Name, (*substitutedType).Name(),
		)
	}

	switch structFieldType.Kind() {
	case reflect.Struct:
		if nil != substitutedType {
			return tm.goToGraphqlType(*substitutedType)
		}
		return tm.goToGraphqlType(structFieldType)
	case reflect.Slice:
		structFieldType = structFieldType.Elem()
		if nil != substitutedType {
			structFieldType = *substitutedType
		}
		switch structFieldType.Kind() {
		case reflect.Struct:
			output, err = tm.goToGraphqlType(structFieldType)
			if nil != err {
				return
			}
			log.Info(tm.indent(), structFieldType, " will be a list of a struct.")
			output = graphql.NewList(output)
			return
		case reflect.Interface:
			output, err = tm.faceToAny(structFieldType)
			if nil != err {
				return
			}
			log.Info(tm.indent(), structFieldType.Name(), " will be a list of an interface")
			output = graphql.NewList(output)
			return
		default:
			output, err = tm.kindOrTypeToGraphqlScalar(structFieldType, structField.Name)
			if nil != err {
				return
			}
			log.Info(tm.indent(), structFieldType.Name(), " will be a list of a scalar")
			output = graphql.NewList(output)
			return
		}
	case reflect.Interface:
		if nil != substitutedType {
			structFieldType = *substitutedType
		}
		return tm.faceToAny(structFieldType)
	}
	if nil != substitutedType {
		structFieldType = *substitutedType
	}
	output, err = tm.kindOrTypeToGraphqlScalar(structFieldType, structField.Name)
	return
}

func (tm *typeMapper) faceToAny(Type reflect.Type) (output graphql.Output, err error) {
	//	output = graphql.NewObject(graphql.ObjectConfig{})
	methodCount := Type.NumMethod()

	// following does not always work and so is disabled
	if true == false && 0 != methodCount {
		Type = Type.Method(0).Type.Out(0)
		log.Printf("%vhackinglly using the return type from method 0 %v %T;", tm.indent(), Type, Type)
		return tm.goToGraphqlType(Type.(reflect.Type))
	}
	output = Any
	return
}

func (tm *typeMapper) kindOrTypeToGraphqlScalar(Type reflect.Type, fieldName string) (scalar *graphql.Scalar, err error) {
	switch Type {
	case reflect.TypeOf(primitive.ObjectID{}):
		scalar = BSON
		return
	case reflect.TypeOf(time.Time{}):
		scalar = graphql.DateTime
		return
	}
	return tm.kindToGraphqlScalar(Type.Kind(), fieldName)
}

func (tm *typeMapper) kindToGraphqlScalar(kind reflect.Kind, fieldName string) (scalar *graphql.Scalar, err error) {

	switch kind {
	case reflect.Bool:
		scalar = graphql.Boolean

	case reflect.Int:
		fallthrough
	case reflect.Int8:
		fallthrough
	case reflect.Int16:
		fallthrough
	case reflect.Int32:
		scalar = graphql.Int
		//baseInput(htmlInfo, crumbs, fieldName)

	case reflect.Int64:
		scalar = Int64
		//baseInput(htmlInfo, crumbs, fieldName)

	case reflect.Uint:
		fallthrough
	case reflect.Uint8:
		fallthrough
	case reflect.Uint16:
		fallthrough
	case reflect.Uint32:
		scalar = graphql.Int
		//baseInput(htmlInfo, crumbs, fieldName)

	case reflect.Uint64:
		scalar = Uint64
		//baseInput(htmlInfo, crumbs, fieldName)

	case reflect.Float32:
		fallthrough
	case reflect.Float64:
		scalar = graphql.Float
		//baseInput(htmlInfo, crumbs, fieldName)

	case reflect.String:
		scalar = graphql.String
		//baseInput(htmlInfo, crumbs, fieldName)

	case reflect.Complex64:
		fallthrough
	case reflect.Complex128:
		fallthrough
	case reflect.Array:
		fallthrough
	case reflect.Chan:
		fallthrough
	case reflect.Func:
		fallthrough
	case reflect.Map:
		fallthrough
	default:
		log.Infof("%vDon't know how to map Go kind %v to graphql kind", tm.indent(), kind)
		log.Infof("%vAm hacking %v to graphql string", tm.indent(), kind)
		scalar = graphql.String
	}
	return
}

var notImpl = "notImplemented"

func coerceNotImplemented(value interface{}) interface{} {
	return notImpl
}

var notImplemented = graphql.NewScalar(graphql.ScalarConfig{
	Name:       "NotImplemented",
	Serialize:  coerceNotImplemented,
	ParseValue: coerceNotImplemented,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		return notImpl
	},
})

func coerceInt64(value interface{}) interface{} {
	if v, ok := value.(int64); ok {
		return v
	}
	return nil
}

var Int64 = graphql.NewScalar(graphql.ScalarConfig{
	Name:       "Int64",
	Serialize:  coerceInt64,
	ParseValue: coerceInt64,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.IntValue:
			if i, err := strconv.ParseInt(valueAST.Value, 10, 64); err == nil {
				return i
			}
		}
		return nil
	},
})

var ObjectID = graphql.NewScalar(graphql.ScalarConfig{
	Name: "ObjectID",
	Serialize: func(value interface{}) interface{} {
		if v, ok := value.(primitive.ObjectID); ok {
			return v.Hex()
		}
		return nil
	},
	ParseValue: func(value interface{}) interface{} {
		if v, ok := value.(string); ok {
			oid, err := primitive.ObjectIDFromHex(v)
			if nil != err {
				return nil
			}
			return oid
		}
		return nil
	},
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.StringValue:
			oid, err := primitive.ObjectIDFromHex(valueAST.Value)
			if nil != err {
				return nil
			}
			return oid
		}
		return nil
	},
})

func coerceUint64(value interface{}) interface{} {
	if v, ok := value.(uint64); ok {
		return v
	}
	return nil
}

var Uint64 = graphql.NewScalar(graphql.ScalarConfig{
	Name:       "Uint64",
	Serialize:  coerceUint64,
	ParseValue: coerceUint64,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.IntValue:
			if i, err := strconv.ParseUint(valueAST.Value, 10, 64); err == nil {
				return i
			}
		}
		return nil
	},
})

// Any reflects the Go lang interface Kind as a string of JSON.
var Any = graphql.NewScalar(graphql.ScalarConfig{
	Name: "Any",
	Serialize: func(value interface{}) interface{} {
		if bytes, err := json.Marshal(value); nil == err {
			return string(bytes)
		}
		return nil
	},
	ParseValue: func(value interface{}) interface{} {
		var v map[string]interface{}
		if err := json.Unmarshal(value.([]byte), &v); nil != err {
			return v
		}
		return nil
	},
	ParseLiteral: func(valueAST ast.Value) interface{} {
		value := valueAST.GetValue()
		var v map[string]interface{}
		if err := json.Unmarshal(value.([]byte), &v); nil != err {
			return v
		}
		return nil
	},
})

func null(value interface{}) interface{} {
	return nil
}

// Null is nil type definition.
var Null = graphql.NewScalar(graphql.ScalarConfig{
	Name:        "Null",
	Description: "a static null value",
	Serialize:   null,
	ParseValue:  null,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		return nil
	},
})

var BSON = graphql.NewScalar(graphql.ScalarConfig{
	Name:        "BSON",
	Description: "The `bson` scalar type represents a BSON Object.",
	// Serialize serializes `primitive.ObjectID` to string.
	Serialize: func(value interface{}) interface{} {
		switch value := value.(type) {
		case primitive.ObjectID:
			return value.Hex()
		case *primitive.ObjectID:
			v := *value
			return v.Hex()
		default:
			return nil
		}
	},
	// ParseValue parses GraphQL variables from `string` to `primitive.ObjectID`.
	ParseValue: func(value interface{}) interface{} {
		switch value := value.(type) {
		case string:
			objectID, err := primitive.ObjectIDFromHex(value)
			if nil != err {
				log.Info(err)
			}
			return objectID
		case *string:
			objectID, err := primitive.ObjectIDFromHex(*value)
			if nil != err {
				log.Info(err)
			}
			return objectID
		default:
			return nil
		}
	},
	// ParseLiteral parses GraphQL AST to `primitive.ObjectID`.
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.StringValue:
			stringVal, err := primitive.ObjectIDFromHex(valueAST.Value)
			if nil != err {
				log.Info(err)
			}
			return stringVal
		}
		return nil
	},
})
