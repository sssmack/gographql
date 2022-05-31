// package gographql translates Go struct types to Graphql types.
/*
Some of the notable features are:

The translation can handle structs that declare fields of its own type.

A field tag annotation may be used to direct the translation to use a type other than the type that the field was declared as in the structure. Use a 'replaceTypeWith' tag for that purpose.

example:
			type VirtualMachineSnapshot struct {
				ExtensibleManagedObject

				Config        types.VirtualMachineConfigInfo `mo:"config" required:"true" description:"Information about the configuration of this virtual machine when this snapshot was\n  taken.\n  \n  The datastore paths for the virtual machine disks point to the head of the disk\n  chain that represents the disk at this given snapshot. The fileInfo.fileLayout\n  field is not set."`
				ChildSnapshot []types.ManagedObjectReference `mo:"childSnapshot" replaceTypeWith:"VirtualMachineSnapshot" required:"false" description:"All snapshots for which this snapshot is the parent.\n      \nSince vSphere API 4.1"`
				Vm            types.ManagedObjectReference   `mo:"vm" replaceTypeWith:"VirtualMachine" required:"true" description:"The virtual machine for which the snapshot was taken.\n      \nSince vSphere API 6.0"`
			}

Tag annotations may contain a tag named 'description'.  The value will be used for the description attribute of the graphql type.


The package uses the viper module of configuration options and logrus for logging.
*/
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
	graphqlOutput targetType = iota
	graphqlInput
)

// String returns the string representation of targetType
func (tt targetType) String() (name string) {
	switch tt {
	case graphqlOutput:
		name = "graphqlOutput"
	case graphqlInput:
		name = "graphqlInput"
	}
	return
}

// ReplaceTypeWith is the name of the key for a field tag key/value pair
// where the value names the type to substitute for the type that the field is
// declared with in the struct type definition.
// Use a TypeReplacer to resolve the value to the actual type.
var ReplaceTypeWith = "replaceTypeWith"
var (
	reStub       = regexp.MustCompile(`(.*)Stub`)
	reList       = regexp.MustCompile(`\[(.*)Stub\]`)
	RENonNull    = regexp.MustCompile(`(.*)Stub\!`)
	reReturnsPtr = regexp.MustCompile(`\(\) \*`)
	objectMapper = NewTypeMapper()
	indentBuf    [10000]byte
	log          = logrus.New()
)

func init() {
	for i := 0; i < len(indentBuf); i++ {
		indentBuf[i] = ' '
	}
}

type typeMapper struct {
	graphqlTypes        map[string]graphql.Type
	parentTypes         map[string]bool
	level               uint
	typeReplacer        TypeReplacer
	fieldResolverFinder FieldResolverFinder
	targetType          targetType
}

// NewTypeMapper creates a new type mapper.
// A type many be defined only once in a schema and so typically one type mapper is used for all
// go struct translations that are required for the schema.
func NewTypeMapper() (tm typeMapper) {
	tm = typeMapper{
		graphqlTypes:        map[string]graphql.Type{},
		parentTypes:         map[string]bool{},
		typeReplacer:        defaultTypeReplacer{},
		fieldResolverFinder: defaultFieldResolverFinder{},
	}
	return tm
}

// A FieldResolverFinder provides the GetResolver method. Given a field type name, the method returns the graphql resolver function for that type, or nil if no function was found.
// substituteTypeName is made available to the method.
// Most types have built-in resolvers that translate the native code (Go) representation to a graphql representation.  A case for using this would be if there is, for example, data retrieval involved.
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

// SetDescription sets the field description on the given type.
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

// GoToGraphqlOutput produces a graphql output type from a Go structure type.
// If the structure has already been marshalled, the one that was found is returned.
func GoToGraphqlOutput(goStruct interface{}) (object *graphql.Object, err error) {
	return objectMapper.GoToGraphqlOutput(goStruct)
}

// GoToGraphqlOutput produces a graphql output type from a Go structure type.
// If the structure has already been marshalled, the one that was found is returned.
func (tm *typeMapper) GoToGraphqlOutput(goStruct interface{}) (object *graphql.Object, err error) {
	tm.targetType = graphqlOutput
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
		err = fmt.Errorf("got type %T; expected type graphql.Object", graphqlType)
		log.Error(err)
		return
	}
	return
}

// GoToGraphqlInput produces a graphql input type from a Go structure type.
// If the structure has already been marshalled, the one that was found is returned.
func GoToGraphqlInput(goStruct interface{}) (inputObject *graphql.InputObject, err error) {
	return objectMapper.GoToGraphqlInput(goStruct)
}

// GoToGraphqlInput produces a graphql input type from a Go structure type.
// If the structure has already been marshalled, the one that was found is returned.
func (tm *typeMapper) GoToGraphqlInput(goStruct interface{}) (inputObject *graphql.InputObject, err error) {
	tm.targetType = graphqlInput
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
		err = fmt.Errorf("got type %T; expected type graphql.InputObject", graphqlType)
		log.Error(err)
		return
	}
	return
}
func getType(tm *typeMapper, typeName, kindName string) (fieldType graphql.Type, err error) {
	fieldType, exists := tm.graphqlTypes[typeName]
	if !exists {
		err = fmt.Errorf(`%v %v object not found for typeName "%v"`, tm.indent(), tm.level, typeName)
		log.Error(err)
		return
	}
	if words := reList.FindStringSubmatch(kindName); nil != words {
		typeName = words[1]
		fieldType = graphql.NewList(fieldType)
	}
	if words := RENonNull.FindStringSubmatch(kindName); nil != words {
		typeName = words[1]
		fieldType = graphql.NewNonNull(fieldType)
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
		err = errors.New(`structure name cannot be ""`)
		return
	}

	var fields interface{}
	if tm.targetType == graphqlInput {
		fields = graphql.InputObjectConfigFieldMap{}
		structureName = structureName + "_Input"
	}
	graphqlType, defined := tm.graphqlTypes[structureName]
	if defined {
		log.Infof(`%vType "%v" already defined; returning that one.`, tm.indent(), structureName)
		return
	}
	if tm.targetType == graphqlOutput {
		fields = graphql.Fields{}
	}
	if _, exists := tm.parentTypes[structureName]; exists { // this Type is a child of itself
		log.Infof(
			`%vStruct "%v" is nested in itself and so am inserting a stub/reference for resolution later.`,
			tm.indent(), structureName,
		)
		typeName := structureName + "Stub"
		fieldName := "aField" // every object must have at least one field.
		switch fields := fields.(type) {
		case graphql.Fields:
			fields[fieldName] = &graphql.Field{Name: fieldName, Type: graphql.Int}
			graphqlType = graphql.NewObject(graphql.ObjectConfig{Name: typeName, Fields: fields})
			tm.graphqlTypes[structureName] = graphqlType
		case graphql.InputObjectConfigFieldMap:
			fields[fieldName] = &graphql.InputObjectFieldConfig{Type: graphql.Int, DefaultValue: nil}
			graphqlType = graphql.NewInputObject(graphql.InputObjectConfig{Name: typeName, Fields: fields})
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
						words := reStub.FindStringSubmatch(stubbedTypeName)
						if nil == words {
							continue
						}
						delete(obj.Fields(), fieldKey)
						typeName := words[1]
						kindName := fmt.Sprintf("%v", reflect.ValueOf(fieldDef.Type))
						fieldType, err := getType(tm, typeName, kindName)
						if nil != err {
							log.Warn(err)
							continue
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
						words := reStub.FindStringSubmatch(stubbedTypeName)
						if nil == words {
							continue
						}
						delete(obj.Fields(), fieldKey)
						typeName := words[1]
						kindName := fmt.Sprintf("%v", reflect.ValueOf(fieldDef.Type))
						fieldType, err := getType(tm, typeName, kindName)
						if nil != err {
							log.Warn(err)
							continue
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

	numFieldsMarshalled := 0
	for fieldNumber := 0; fieldNumber < structure.NumField(); fieldNumber++ {
		structField := structure.Field(fieldNumber)
		log.Infof("%v %v %v %v.%v", tm.indent(), tm.level, fieldNumber, structureName, structField.Name)
		graphqlFieldType, err := tm.goFieldToGraphqlType(structField, structureName)
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
		substituteTypeName := structField.Tag.Get(ReplaceTypeWith)
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
		err = fmt.Errorf(`struct "%v" had 0 marshalable fields; skipping it`, structureName)
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

func (tm typeMapper) goFieldToGraphqlType(structField reflect.StructField, structName string) (output graphql.Type, err error) {
	structFieldType := structField.Type
	if structFieldType.Kind() == reflect.Ptr {
		structFieldType = structFieldType.Elem()
	}

	substituteTypeName := structField.Tag.Get(ReplaceTypeWith)
	substitutedType := tm.typeReplacer.GetType(substituteTypeName)
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
		scalar = ObjectID
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

	case reflect.Int64:
		scalar = Int64

	case reflect.Uint:
		fallthrough
	case reflect.Uint8:
		fallthrough
	case reflect.Uint16:
		fallthrough
	case reflect.Uint32:
		scalar = graphql.Int

	case reflect.Uint64:
		scalar = Uint64
		//baseInput(htmlInfo, crumbs, fieldName)

	case reflect.Float32:
		fallthrough
	case reflect.Float64:
		scalar = graphql.Float

	case reflect.String:
		scalar = graphql.String

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

// Int64 reflects the Go Int64 to a graphql output type and vice versa.
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

// ObjectID reflects the bson ObjectID to a graphql output type and vice versa.
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

// Uint64 reflects the Go Uint64 kind to a graphql output type and vice versa.
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

// Any reflects the Go lang interface Kind to a string of a JSON document and vice versa.
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
