package gographql

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/spf13/viper"
	"gitlab.issaccorp.net/mda/tower/logger"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var (
	log                = logger.DefaultLogger
	REmor              = regexp.MustCompile(`.*ManagedObjectReference.*`)
	REstub             = regexp.MustCompile(`(.*)Stub`)
	RElist             = regexp.MustCompile(`\[(.*)\]`)
	REtype             = regexp.MustCompile(` [\[\]\*](.*)`)
	objectMapper       = newMapper()
	SubstitutedTypeKey = "substitutedType"
)

type TypeReflector interface {
	GetReflectType(string) reflect.Type
}

type Input struct {
	object  *graphql.InputObject
	html    *strings.Builder
	dataMap interface{}
}

type objectMap struct {
	allObjectTypes      map[string]*graphql.Object
	allInputObjectTypes map[string]Input
	parentTypes         map[string]bool
	level               uint
	indentBuf           [10000]byte
	methods             map[string]string
	indexValues         string
	sliceDepth          uint
	typeInstance        uint64
	substituedTypes     map[string]graphql.FieldResolveFn
	typeReflector       TypeReflector
}

func SetLogger(l logger.Logger) {
	*Log = l
}

// Use Setdescription() when the package having the Go structure is not
// owned by this development group and the struct field has no description
// in the field annotation.
func SetDescription(i interface{}, fieldName, text string) {
	switch object := i.(type) {
	case *graphql.Object:
		if _, ok := object.Fields()[fieldName]; ok {
			object.Fields()[fieldName].Description = text
		}
	case *graphql.InputObject:
		if _, ok := object.Fields()[fieldName]; ok {
			object.Fields()[fieldName].PrivateDescription = text
		}
	default:
		log.Error("I don't know about type %T!\n", object)
	}
}

func newMapper() (mapper objectMap) {
	mapper = objectMap{
		allObjectTypes:      map[string]*graphql.Object{},
		allInputObjectTypes: map[string]Input{},
		parentTypes:         map[string]bool{},
		level:               0,
		methods:             map[string]string{},
		indexValues:         "abcdefghijklmnopqrstuvwzyz",
	}
	for i := 0; i < len(mapper.indentBuf); i++ {
		mapper.indentBuf[i] = ' '
	}
	return mapper
}

func (mapper *objectMap) SetGraphQLFields(fields map[string]graphql.FieldResolveFn) {
	mapper.substituedTypes = fields
}

func (mapper *objectMap) SetTypeRegistryProvider(provider TypeReflector) {
	mapper.typeReflector = provider
}

// GetType returns either nil or the object known by name.
func GetType(name string) (object *graphql.Object) {
	object = objectMapper.allObjectTypes[name]
	return
}

func (m objectMap) prefix() string {
	return string(m.indentBuf[0 : 3*m.level])
}

// Marshal "marshals" a Go Lang structure to a graphQL object.
// A "warning" level log message is written if the structure has already been marshalled.
// In that case, the existing graphQL object is returned and err is set to FieldRedefinition.
//    Some affects from field annotations:
//       if the "description" tag is found, the Description field of the object is assigned its value.
//       if the "mor" tag is found, reflection for the field will be done using its value, which is the type of a struct. That is in contrast with the normal path of processing which is to reflect on the type of the field.
func MarshalObject(i interface{}) (object *graphql.Object, err error) {
	logLevel := log.GetLevel()
	defer func() { log.SetLevel(logLevel) }()
	goToGraphqlLogLevel := viper.GetString("goToGraphqlLogLevel")
	err = log.SetLevel(goToGraphqlLogLevel)
	if nil != err {
		return
	}
	// log.SetFlags(log.Llongfile | log.LstdFlags)
	if nil == objectMapper.typeReflector {
		log.Warn("The typeReflector is nil")
	}
	return makeObject(objectMapper, i)
}

func makeObject(mapper objectMap, i interface{}) (object *graphql.Object, err error) {
	structType, ok := i.(reflect.Type)
	if !ok {
		structType = reflect.TypeOf(i)
	}
	if reflect.Struct != structType.Kind() {
		err = errors.New("The reflect.Type argument was not of Kind reflect.Struct.")
		log.Println(mapper.prefix(), err)
		return nil, err
	}
	if gqlobject, defined := mapper.allObjectTypes[structType.Name()]; defined {
		log.Warn(mapper.prefix(), "This type has already been defined, am using it, but its definition may be different than this one that you are defining a-new.", defined)
		return gqlobject, nil
	}
	fields := graphql.Fields{}
	thisStructName := structType.Name()
	if "" == thisStructName {
		err = errors.New("the struct type name is empty; skipping this struct")
		log.Println(mapper.prefix(), err)
		return nil, err
	}
	if _, exists := mapper.parentTypes[thisStructName]; exists {
		log.Println(mapper.prefix(),
			"Already reflecting on", thisStructName, "and so am inserting a ref to its type for resolution later.",
		)
		stubStructName := thisStructName + "Stub"
		if object, exists := mapper.allObjectTypes[stubStructName]; exists {
			return object, nil
		}
		// Assign this field a stub graphql field that will be resolved during defer where 0 == m.level
		// err = fmt.Errorf("Am skipping child type %v because it is a parent.", structType.Name())
		// log.Println(string(m.indent[0:3*m.level]), err)

		name := "bogus"
		fields[name] = &graphql.Field{
			Name: name,
			Type: graphql.Int,
			/*
				Resolve: fn,
				DeprecationReason: getTagValue(structField, "deprecationReason"),
				Description:       getTagValue(structField, "description"),
			*/
		}
		object = graphql.NewObject(
			graphql.ObjectConfig{
				Name:   stubStructName,
				Fields: fields,
			},
		)
		mapper.allObjectTypes[stubStructName] = object
		return object, nil
	}
	mapper.parentTypes[thisStructName] = true
	mapper.level++
	defer func() {
		if nil != err {
			log.Println(mapper.prefix(), thisStructName, "object=", object, "error=", err)
		} else {
			log.Println(mapper.prefix(), thisStructName, "object=", object)
		}
		delete(mapper.parentTypes, thisStructName)
		mapper.level--

		if 0 == mapper.level { // release memory and resolve the stubbed fields that are contained in each type.
			for _, object := range mapper.allObjectTypes {
				for key, fieldDef := range object.Fields() {
					// log.Println("type", typeName, "field:", key, "fieldType:", fieldDef.Type)
					typeName := fieldDef.Type.String()
					isList := false
					listWords := RElist.FindStringSubmatch(typeName)
					if 1 < len(listWords) {
						typeName = listWords[1]
						isList = true
					}
					typeNameWords := REstub.FindStringSubmatch(typeName)
					if 2 > len(typeNameWords) {
						continue // this field is not a stubbed type.
					}
					log.Println("typeNameWords is:", typeNameWords)
					targetObject := object
					var sourceObject graphql.Output
					sourceObject = mapper.allObjectTypes[typeNameWords[1]]
					if isList {
						sourceObject = graphql.NewList(sourceObject)
					}
					////log.Println("source is", sourceObject, "TypeName is", TypeNameWords[1])
					log.Println("In object type", targetObject, ", replaced field named", key, "having type", typeName, "with type", sourceObject, "is a list=", isList)
					//					log.Println("================>", fieldDef.Resolve, key)
					targetObject.AddFieldConfig(key, &graphql.Field{
						Name:              key,
						Type:              sourceObject,
						Args:              graphql.FieldConfigArgument{},
						Resolve:           fieldDef.Resolve,
						DeprecationReason: fieldDef.DeprecationReason,
						Description:       fieldDef.Description,
					})
				}
			}
			mapper.parentTypes = map[string]bool{}
		}
	}()
	/*
		if fieldType, exists := m.allObjectTypes[thisStructName]; exists {
			log.Warn(m.prefix(), "This type has already been defined, am using it, and its definition may be different!", thisStructName)
			return fieldType, nil
		}
	*/
	fieldCount := structType.NumField()
	if 0 == fieldCount {
		log.Println(mapper.prefix(), "IGNORING", structType.Name(), "; the struct is empty.")
		return object, errors.New("zero fields")
	}
	log.Println(mapper.prefix(), "begin reflecting on", thisStructName)
	for i := 0; i < fieldCount; i++ {
		structField := structType.Field(i)
		required := structField.Tag.Get("required")
		//		log.Println(m.prefix(), thisStructName, ".", structField.Name)
		output, face, err := mapper.goToGraph(structField, structType.Name())
		if nil != err {
			log.Println(mapper.prefix(),
				thisStructName, ".", structField.Name, "IGNORING", err,
			)
			continue
		}
		var fieldResolver graphql.FieldResolveFn
		if face {
			fieldResolver = Face
		}
		substitutedTypeName, ok := structField.Tag.Lookup(SubstitutedTypeKey)
		if ok {
			if fn, ok := mapper.substituedTypes[substitutedTypeName]; ok {
				fieldResolver = fn
			}
		} else {
			if fn, ok := mapper.substituedTypes[structField.Type.String()]; ok {
				fieldResolver = fn
			}
		}

		/*
			matched := REmor.MatchString(structField.Type.String())
			if matched {
				if Type, ok := structField.Tag.Lookup("type"); ok {
					switch Type {
					case "ManagedEntity":
						fieldResolver = ManagedEntity
					default:
						fieldResolver = Mor
					}
				}
			}
		*/
		fieldDescription := structField.Tag.Get("description")
		if "true" == required {
			output = graphql.NewNonNull(output)
		}
		if structField.Type.Name() == "AnyType" {
			fieldResolver = AnyTypeResolver
		}
		/*
			if "ManagedEntity" == structField.Name {
				fn = resolvers.ManagedEntity
			}
		*/
		fields[structField.Name] = &graphql.Field{
			Name:    structField.Name,
			Type:    output,
			Resolve: fieldResolver,
			/*
				DeprecationReason: getTagValue(structField, "deprecationReason"),
			*/
			Description: fieldDescription,
		}
		log.Println(mapper.prefix(), thisStructName, ".", structField.Name, ", type:", output, ", resolver:", fieldResolver, ", required:", required)
	}
	log.Println(mapper.prefix(), "end reflecting on", thisStructName)

	if 0 == len(fields) {
		err = errors.New("Mapped zero fields.")
		log.Println(mapper.prefix(), "IGNORING", thisStructName, err)
		return nil, err
	}
	//------------------------------------//
	object = graphql.NewObject(
		graphql.ObjectConfig{
			Name:   thisStructName,
			Fields: fields,
		},
	)
	mapper.allObjectTypes[thisStructName] = object
	return object, nil
}
func (mapper objectMap) faceToGraph(Type reflect.Type) (output graphql.Output, err error) {
	methodCount := Type.NumMethod()
	if 0 == methodCount {
		err := errors.New("IGNORING" + Type.Name() + "; the interface is empty.")
		log.Println(mapper.prefix(), err)
		return output, err
	}
	returnType1 := Type.Method(0).Type.String()
	listWords := strings.Split(returnType1, ".")
	if 2 > len(listWords) {
		err := fmt.Errorf("could find the type returned by %s %s", Type.Method(0).Type.String())
		log.Println(mapper.prefix(), err)
		return output, err
	}
	typeName := listWords[1]
	object, err := makeObject(mapper, mapper.typeReflector.GetReflectType(typeName))
	if nil != err {
		log.Println(mapper.prefix(), err)
		return object, err
	}
	return object, err
}

func (mapper objectMap) goToGraph(structField reflect.StructField, structName string) (output graphql.Output, face bool, err error) {
	Type := structField.Type
	isPtr := false
	if Type.Kind() == reflect.Ptr {
		Type = Type.Elem()
		isPtr = true
		log.Println(mapper.prefix(), Type.Name(), "Is Ptr:", isPtr)
		log.Println(mapper.prefix(), "Have de-referenced", Type.Name())
	}

	if Type.Name() == "AnyType" {
		return graphql.String, false, nil
	}
	if reflect.TypeOf(primitive.ObjectID{}) == Type {
		return BSON, false, nil
	}
	if "Time" == Type.Name() {
		return graphql.DateTime, false, nil
	}
	t := Type

	var mObjType reflect.Type
	matched := REmor.MatchString(Type.String())
	if matched {
		moType, ok := structField.Tag.Lookup("type")
		if ok {
			mObjType = mapper.typeReflector.GetReflectType(moType)
			log.Println("using", mObjType, "in place of ManagedObjectReference", structName, structField.Name)
		}
	}

	if nil != mObjType {
		t = mObjType
		log.Println(mapper.prefix(), "In struct named", structName, "replaced field named", structField.Name, "of type MOR with", t)
	}

	//	log.Printf("%s: (type %s)", structName, Type.Kind().String())
	switch Type.Kind() {
	case reflect.Struct:
		if 0 == t.NumField() {
			return graphql.String, false, err
			//return Null, false, err
		}
		output, err = makeObject(mapper, t)
		if nil != err {
			return
		}
		return

	case reflect.Slice:
		switch Type.Elem().Kind() {
		case reflect.Struct: // is a slice of structs
			if nil == mObjType {
				t = Type.Elem()
			}
			log.Println(mapper.prefix(), t, "will be a list of struct.")
			if 0 == t.NumField() {
				output = graphql.NewList(Null)
				return
			}
			output, err = makeObject(mapper, t)
			if nil != err {
				log.Println(mapper.prefix(), err)
				return
			}
			output = graphql.NewList(output)
			return
		case reflect.Interface:
			Type = Type.Elem()
			log.Println(mapper.prefix(), Type.Name(), "will be a list of interface")
			if 0 == Type.NumMethod() {
				output = graphql.NewList(Any)
				return
			}
			output, err = mapper.faceToGraph(Type)
			if nil != err {
				log.Println(mapper.prefix(), err)
				return output, true, err
			}
			output = graphql.NewList(output)
			return output, true, err
		default:
			var scalar *graphql.Scalar
			log.Println(mapper.prefix(), Type.Elem().Kind(), "will be a list.")
			scalar, _, err = mapper.goToGraphqlScalar(Type.Elem().Kind(), structField.Name, nil, "", nil)
			if nil != err {
				log.Println(mapper.prefix(), "list will not be generated, reason;", err)
				return
			}
			output = graphql.NewList(scalar)
			return
		}
	case reflect.Interface:
		if 0 == Type.NumMethod() {
			// log.Printf(`in here, type is "%v"`, Type.Name())
			return Any, false, err
			//return Null, false, err
		}
		output, err = mapper.faceToGraph(Type)
		if nil != err {
			log.Println(mapper.prefix(), err)
		}
		return output, true, err
	}
	scalar, _, err := mapper.goToGraphqlScalar(Type.Kind(), structField.Name, nil, "", nil)
	return scalar, face, err
}

func (m *objectMap) goToGraphqlScalar(kind reflect.Kind, fieldName string, htmlInfo *HTMLinfo, crumbs string, sliceIndex *string) (scalar *graphql.Scalar, init interface{}, err error) {

	crumbs = crumbs + "." + fieldName
	if nil != sliceIndex {
		crumbs = fmt.Sprintf("%s[%s]", crumbs, *sliceIndex)
	}
	if nil != htmlInfo {
		htmlInfo.description = strings.TrimSpace(htmlInfo.description)
	}
	baseInput := func(htmlInfo *HTMLinfo, crumbs, fieldName string) {
		if nil != htmlInfo {
			htmlInfo.form.WriteString(
				fmt.Sprintf(
					`<ValidationProvider> <base-input %v v-model="%v" label="%v">`,
					htmlInfo.required,
					crumbs,
					fieldName,
				),
			)
			if 0 < len(htmlInfo.description) {
				htmlInfo.form.WriteString(
					fmt.Sprintf(
						"<template v-slot:helperText> <small>%v</small> </template>", htmlInfo.description,
					),
				)
			}
			htmlInfo.form.WriteString("</base-input> </ValidationProvider>")
		}
	}
	//	log.Println(crumbs)
	/*
		if nil != htmlInfo {
			htmlInfo.description = ""
		}
	*/
	if nil != htmlInfo {
		htmlInfo.description = html.EscapeString(htmlInfo.description)
	}
	switch kind {
	case reflect.Bool:
		scalar = graphql.Boolean
		if nil != htmlInfo {
			htmlInfo.form.WriteString(
				fmt.Sprintf(
					`<ValidationProvider> <base-checkbox %v v-model="%v"> %v </base-checkbox> </ValidationProvider>`,
					htmlInfo.required, crumbs, fieldName),
			)
			if 0 < len(htmlInfo.description) {
				htmlInfo.form.WriteString(fmt.Sprintf(`<p style="color:white" ><small>%v</small></p>`, htmlInfo.description))
			}
		}
		init = false

	case reflect.Int:
		fallthrough
	case reflect.Int8:
		fallthrough
	case reflect.Int16:
		fallthrough
	case reflect.Int32:
		scalar = graphql.Int
		baseInput(htmlInfo, crumbs, fieldName)
		init = 0

	case reflect.Int64:
		scalar = Int64
		baseInput(htmlInfo, crumbs, fieldName)
		init = 0

	case reflect.Uint:
		fallthrough
	case reflect.Uint8:
		fallthrough
	case reflect.Uint16:
		fallthrough
	case reflect.Uint32:
		scalar = graphql.Int
		baseInput(htmlInfo, crumbs, fieldName)
		init = 0

	case reflect.Uint64:
		scalar = Uint64
		baseInput(htmlInfo, crumbs, fieldName)
		init = 0

	case reflect.Float32:
		fallthrough
	case reflect.Float64:
		scalar = graphql.Float
		baseInput(htmlInfo, crumbs, fieldName)
		init = 0.0

	case reflect.String:
		scalar = graphql.String
		baseInput(htmlInfo, crumbs, fieldName)
		init = ""

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
		log.Printf("Don't know how to map Go kind %v to graphql kind!\n", kind)
		log.Printf("Am hacking %v to graphql string!\n", kind)
		scalar = graphql.String
		baseInput(htmlInfo, crumbs, fieldName)
		init = ""
		/*
			scalar = notImplemented
			init = nil
		*/
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
		if bytes, err := json.Marshal(value); nil != err {
			return string(bytes)
		}
		return nil
	},
	ParseLiteral: func(valueAST ast.Value) interface{} {
		value := valueAST.GetValue()
		if bytes, err := json.Marshal(value); nil != err {
			return string(bytes)
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
			stringVal, err := primitive.ObjectIDFromHex(value)
			if nil != err {
				log.Println(err)
			}
			return stringVal
		case *string:
			stringVal, err := primitive.ObjectIDFromHex(*value)
			if nil != err {
				log.Println(err)
			}
			return stringVal
		default:
			return nil
		}
		return nil
	},
	// ParseLiteral parses GraphQL AST to `primitive.ObjectID`.
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.StringValue:
			stringVal, err := primitive.ObjectIDFromHex(valueAST.Value)
			if nil != err {
				log.Println(err)
			}
			return stringVal
		}
		return nil
	},
})
