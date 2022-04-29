package gographql

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/graphql-go/graphql"
	"github.com/spf13/viper"
)

type Arg struct {
	occurance    uint
	initialValue string
}

var (
	componentName     *string
	inputObjectMapper = NewMapper()
)

type HTMLinfo struct {
	form                  *strings.Builder
	required, description string
}

func cullBytes(str string) string {
	b := make([]byte, len(str))
	var bl int
	for i := 0; i < len(str); i++ {
		c := str[i]
		if (c >= 65 && c <= 90) || (c >= 97 && c <= 122) {
			b[bl] = c
			bl++
		}
	}
	return string(b[:bl])
}

type AlreadyDefined struct {
	name string
}

func (already AlreadyDefined) Error() string {
	return fmt.Sprintf(`Input graphql Field Type "%v" has already been defined; will not re-define.`, already.name)
}

// GetInputType returns either nil or the object known by name.
func GetInputType(name string) (inputObject *graphql.InputObject, err error) {
	inputObject = inputObjectMapper.allInputObjectTypes[name+"_Input"].object
	if nil == inputObject {
		err = errors.New("not found")
	}
	return
}

// MarshalInputObject "marshals" a Go Lang structure to a graphQL InputObject.
//    There are optional struct field tags that that may be used to affect the outcome.
//       if the "description" tag is found, the Description field of the object is assigned its value.
func MarshalInputObject(i interface{}) (inputObject *graphql.InputObject, form *strings.Builder, err error) {
	logLevel := log.GetLevel()
	defer func() { log.SetLevel(logLevel) }()
	goToGraphqlLogLevel := viper.GetString("goToGraphqlLogLevel")
	err = log.SetLevel(goToGraphqlLogLevel)
	if nil != err {
		return
	}
	var (
		structType reflect.Type
		ok         bool
	)
	if structType, ok = i.(reflect.Type); !ok {
		structType = reflect.TypeOf(i)
	}
	if reflect.Ptr == structType.Kind() {
		structType = structType.Elem()
	}
	if reflect.Struct != structType.Kind() {
		err = fmt.Errorf("The reflect.Kind argument was not of Kind reflect.Struct; the Kind is:%v", structType.Kind())
		return nil, nil, err
	}
	fieldName := structType.Name()
	if input, defined := inputObjectMapper.allInputObjectTypes[structType.Name()+"_Input"]; defined {
		log.Warn("This type has already been defined, am using it, but its definition may be different than this one that you are defining a-new.", defined)
		return input.object, input.html, err
	}

	form = &strings.Builder{}
	form.WriteString(`
<template>
  <ValidationObserver v-slot="{ handleSubmit }">
  <form @submit.prevent="handleSubmit(submit)">
		<!--			<pre>Debug: {{ $data}}</pre> -->
	<span>
	<p style="color:white" class="float-left"> Required fields are followed by <strong><abbr title="required">*</abbr></strong> </p>
	<base-button  class="float-right" title="Click to submit this form" native-type="submit">Submit</base-button>
	</span>
		<collapse :multiple-active="true">
	 `)
	inputObjectMapper.methods = map[string]string{}
	culled := cullBytes(fieldName)
	componentName = &culled
	input, dataMap, err := inputObjectMapper.marshalInputObject(i, &fieldName, form, "", nil)
	if nil != err {
		return nil, nil, err
	}
	dataObject, err := json.MarshalIndent(&dataMap, "", "  ")
	allMethods := strings.Builder{}
	for _, v := range inputObjectMapper.methods {
		if 0 < allMethods.Len() {
			allMethods.WriteString(fmt.Sprint(",\n"))
		}
		allMethods.WriteString(v)
	}
	allMethodsString := ""
	if 0 < allMethods.Len() {
		allMethods.WriteString(",")
		allMethodsString = allMethods.String()
	}
	form.WriteString(` 
	 </collapse>
  </form>
  </ValidationObserver>
</template>
  `)
	form.WriteString(
		fmt.Sprintf(
			`
		<script>
		  import { BaseButton, Collapse, CollapseItem, BaseCheckbox, BaseInput } from '../../../index'
		import { extend } from "vee-validate";
		import { required  } from "vee-validate/dist/rules";
		import * as auth from "../../../../util/auth";

		//import { configure } from 'vee-validate';

		extend("required", required);
		/*
		extend("email", email);
		extend("confirmed", confirmed);
		*/

		export default {
		name: "%v",
		  components: {
		  	BaseButton,
		    BaseCheckbox,
			 BaseInput,
			 Collapse,
			 CollapseItem,
		  },
		  data() {
		    return %v ;
		  },
		  methods: {
		  	%v
			submit() {
				let argValue = JSON.stringify( this.%v, null, 2 )
				let query =   `+"`"+`
				mutation {
					 <mutationName>(
						<argName> ${argValue}
					 ) {
						Res {
						  Returnval {
							 Type
							 Value
						  }
						}
					 } 
				 }
				`+"`;"+`
					 alert( query );
		query = query.replace(/"(.*)":/g, '$1:');
		 (async () => {
        let result = await auth.graphQL(query);
        if (result.data.errors && 0 < result.data.errors.length) {
          alert(result.data.errors[0].message);
        }
      })();
        		},
		  },
		};
		</script>
		<style></style>
		`,
			*componentName,
			string(dataObject),
			allMethodsString,
			fieldName,
		),
	)
	return input, form, err
}

func (m Mapper) marshalInputObject(i interface{}, fieldName *string, form *strings.Builder, crumbs string, sliceIndex *string) (inputObject *graphql.InputObject, thisDataMap interface{}, err error) {
	thisDataMap = map[string]interface{}{}
	var (
		structType reflect.Type
		ok         bool
	)
	if structType, ok = i.(reflect.Type); !ok {
		structType = reflect.TypeOf(i)
	}
	if reflect.Ptr == structType.Kind() {
		structType = structType.Elem()
	}
	if reflect.Struct != structType.Kind() {
		err = fmt.Errorf("The reflect.Kind argument was not of Kind reflect.Struct; the Kind is:%v", structType.Kind())
		log.Println(m.prefix(), err)
		return nil, thisDataMap, err
	}
	actualStructTypeName := structType.Name()
	structNameInput := actualStructTypeName + "_Input"
	fields := graphql.InputObjectConfigFieldMap{}
	if "" == actualStructTypeName {
		err = errors.New("the struct type name is empty; skipping this struct")
		log.Println(m.prefix(), err)
		return nil, thisDataMap, err
	}
	//	} else {
	if 0 < len(crumbs) {
		crumbs = crumbs + "." + *fieldName
	} else {
		crumbs = *fieldName
	}
	//}
	if nil != sliceIndex {
		crumbs = fmt.Sprintf("%s[%s]", crumbs, *sliceIndex)
	}

	log.Println(m.prefix(), structNameInput)
	if _, exists := m.parentTypes[structNameInput]; exists {
		log.Println(m.prefix(),
			"Already reflecting on", structNameInput, "and so am inserting a ref to its type for resolution later.",
		)
		stubStructName := structNameInput + "Stub"
		// Assign this field a stub graphql field that will be resolved during defer where 0 == m.level
		// err = fmt.Errorf("Am skipping child type %v because it is a parent.", structType.Name())
		// log.Println(string(m.indent[0:3*m.level]), err)

		name := "bogus"
		fields[name] = &graphql.InputObjectFieldConfig{
			Type:         graphql.Int,
			DefaultValue: nil,
		}
		inputObject = graphql.NewInputObject(
			graphql.InputObjectConfig{
				Name:        stubStructName,
				Fields:      fields,
				Description: "",
			},
		)
		m.allInputObjectTypes[stubStructName] = Input{inputObject, &strings.Builder{}, nil}
		return inputObject, "", nil
	}
	m.parentTypes[structNameInput] = true
	m.level++
	defer func() {
		if nil != err {
			log.Println(m.prefix(), structNameInput, "inputObject=", inputObject, "error=", err)
		} else {
			log.Println(m.prefix(), structNameInput, "inputObject=", inputObject)
		}
		delete(m.parentTypes, structNameInput)
		m.level--
		if 0 == m.level { // release memory and resolve the stubbed fields that are contained in each type.
			for _, input := range m.allInputObjectTypes {
				for key, fieldDef := range input.object.Fields() {
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
					targetObject := input.object

					var sourceObject graphql.Output
					sourceObject = m.allInputObjectTypes[typeNameWords[1]].object
					input.dataMap = m.allInputObjectTypes[typeNameWords[1]].dataMap
					if isList {
						sourceObject = graphql.NewList(sourceObject)
					}
					////log.Println("source is", sourceObject, "TypeName is", TypeNameWords[1])
					log.Println("In inputObject type", targetObject, ", replaced field named", key, "having type", typeName, "with type", sourceObject, "is a list=", isList)
					targetObject.AddFieldConfig(key, &graphql.InputObjectFieldConfig{
						Type:         sourceObject,
						DefaultValue: fieldDef.DefaultValue,
						Description:  fieldDef.Description(),
					})
				}
			}
			m.parentTypes = map[string]bool{}
			m := map[string]interface{}{}
			m[actualStructTypeName] = thisDataMap
			thisDataMap = m
		}
	}()
	fieldCount := structType.NumField()
	if 0 == fieldCount {
		err = fmt.Errorf("IGNORING %v; the struct has zero fields.", structType.Name())
		log.Println(m.prefix(), err)
		return inputObject, thisDataMap, err
	}
	fieldsForm := strings.Builder{}
	for i := 0; i < fieldCount; i++ {
		structField := structType.Field(i)
		required := structField.Tag.Get("required")
		description := structField.Tag.Get("description")
		req := ""
		if "true" == required {
			req = "required"
		}
		htmlInfo := HTMLinfo{&fieldsForm, req, description}
		input, dataMap, err := m.goToGraphInput(structField, structType.Name(), htmlInfo, crumbs)
		if nil != err {
			log.Println(m.prefix(),
				structNameInput, ".", structField.Name, "IGNORING", err,
			)
			continue
		}
		if m, ok := thisDataMap.(map[string]interface{}); ok {
			m[structField.Name] = dataMap
		}
		if "true" == required {
			input = graphql.NewNonNull(input)
		}
		fields[structField.Name] = &graphql.InputObjectFieldConfig{
			Type:         input,
			DefaultValue: nil,
			Description:  description,
		}

		log.Println(
			m.prefix(), structNameInput, ".", structField.Name, "--->",
			input,
			"required=", required,
		)
	}
	if 0 == len(fields) {
		err = errors.New("Mapped zero fields.")
		log.Println(m.prefix(), "IGNORING", structNameInput, err)
		return nil, thisDataMap, err
	}
	form.WriteString(fmt.Sprintf("<collapse-item> <template v-slot:title> %v </template>", *fieldName))
	form.WriteString(fieldsForm.String())
	form.WriteString("</collapse-item>")
	// Did the above work to generate the HTML even though
	// the following may use a previous graphql input obj.
	if input, exists := m.allInputObjectTypes[structNameInput]; exists {
		log.Warn(m.prefix(), "This type has already been defined, am using it, and its definition may be different!", structNameInput)
		return input.object, thisDataMap, nil
	}
	inputObject = graphql.NewInputObject(
		graphql.InputObjectConfig{
			Name:        structNameInput,
			Fields:      fields,
			Description: "",
		},
	)
	m.allInputObjectTypes[structNameInput] = Input{inputObject, &fieldsForm, thisDataMap}
	return inputObject, thisDataMap, nil
}

func (m Mapper) goToGraphInput(structField reflect.StructField, structName string, htmlInfo HTMLinfo, crumbs string) (input graphql.Input, dataMap interface{}, err error) {
	dataMap = map[string]interface{}{}
	Type := structField.Type
	isPtr := false
	if Type.Kind() == reflect.Ptr {
		Type = Type.Elem()
		isPtr = true
		log.Println(m.prefix(), Type.Name(), "Is Ptr:", isPtr)
		log.Println(m.prefix(), "Have de-referenced", Type.Name())
	}

	if "Time" == Type.Name() {
		return graphql.DateTime, dataMap, nil
	}
	if "ObjectID" == Type.Name() {
		return ObjectID, dataMap, nil
	}

	switch Type.Kind() {
	case reflect.Struct:
		return m.marshalInputObject(Type, &structField.Name, htmlInfo.form, crumbs, nil)

	case reflect.Slice:
		Type = Type.Elem() // get the type this slice/list is of
		log.Println(m.prefix(), Type, "will be a list of struct.")
		sliceIndex := m.indexValues[m.sliceDepth : m.sliceDepth+1]
		htmlInfo.form.WriteString(
			fmt.Sprintf(`
				 <div class="card" style="width: 100%%">
					<p>Debug: {{%v.%v}}</p>
              <div class="card-body">
				<div v-for="(f,%s) in %s.%s" v-bind:key="%s">`,
				crumbs, structField.Name,
				sliceIndex, crumbs, structField.Name, sliceIndex,
			),
		)
		m.sliceDepth++
		defer func() {
			htmlInfo.form.WriteString(
				fmt.Sprintf(`
					</div>
			      <span> <base-button @click.prevent="new%s(event, %s.%s)">Add another entry</base-button> </span>
              </div>
            </div>`,
					Type.Name(),
					crumbs, structField.Name,
				),
			)

			m.sliceDepth--
		}()
		switch Type.Kind() {
		case reflect.Struct:
			fieldCount := Type.NumField()
			log.Printf("slice of struct: %v %v ; # of fields is: %v", Type.Name(), structField.Name, fieldCount)
			if 0 == fieldCount {
				input = graphql.NewList(Null)
				return input, []string{}, err
			}
			input, dataMap, err = m.marshalInputObject(Type, &structField.Name, htmlInfo.form, crumbs, &sliceIndex)
			if nil != err {
				log.Error(m.prefix(), err)
				return input, nil, err
			}
			input = graphql.NewList(input)
			//dataObject, err := json.MarshalIndent(&dataMap, "", "  ")
			dataObject, err := json.Marshal(&dataMap)
			if nil != err {
				log.Println(err)
			}
			m.methods[Type.Name()] = fmt.Sprintf(`
				new%v(event, arr) { // an example of a field name needing this is: %v
				      arr.push(%v);
				    }
				  `,
				Type.Name(),
				structField.Name,
				string(dataObject),
			)
			return input, []interface{}{dataMap}, err
		case reflect.Interface: //TODO
			log.Println("slice of interface", Type.Name(), structField.Name)
			log.Println("Field", structField.Name, "is an interface; interfaces are not supported.")
			log.Printf("the interface has %d method(s).\n", Type.NumMethod())
			if 0 < Type.NumMethod() {
				log.Println("hackingly using the return type from method 0;", Type.Method(0).Type.Out(0))
				input, dataMap, err = m.marshalInputObject(Type.Method(0).Type.Out(0), &structField.Name, htmlInfo.form, crumbs, &sliceIndex)
				if nil != err {
					log.Println(m.prefix(), err)
					return nil, nil, err
				}
				return input, []interface{}{dataMap}, err
			}
			log.Println("making the interface value be a string.")
			input = graphql.String
			//  newint64(event, arr) arr
			// arr.push(0)
			m.methods[Type.Name()] = fmt.Sprintf(`
				new%v(event, arr) { // an example of a field name needing this is: %v
				      arr.push(%v);
				    }
				  `,
				Type.Name(),
				structField.Name,
				`""`,
			)
			return input, []string{""}, err
		default:
			log.Println("slice of scalar", Type.Name(), structField.Name)
			// try a slice of scalar
			scalar, init, err := m.goToGraphqlScalar(Type.Kind(), structField.Name, &htmlInfo, crumbs, &sliceIndex)
			if nil != err {
				log.Println(m.prefix(), "list will not be generated, reason;", err)
				return input, nil, err
			}
			if _, ok := init.(string); ok {
				init = `""`
			}
			input = graphql.NewList(scalar)
			m.methods[Type.Name()] = fmt.Sprintf(`
				new%v(event, arr) { // an example name of a field needing this is: %v
				      arr.push(%v);
				    }
				  `,
				Type.Name(),
				structField.Name,
				init,
			)
			if _, ok := init.(string); ok {
				return input, []string{""}, err
			}
			return input, []int64{0}, err
		}
	case reflect.Interface: //TODO
		log.Println("Field", structField.Name, "is an interface; interfaces are not supported.")
		log.Printf("the interface has %d method(s).\n", Type.NumMethod())
		if 0 < Type.NumMethod() {
			log.Println("hackingly using the return type from method 0;", Type.Method(0).Type.Out(0))
			input, dataMap, err = m.marshalInputObject(Type.Method(0).Type.Out(0), &structField.Name, htmlInfo.form, crumbs, nil)
			if nil != err {
				log.Println(m.prefix(), err)
				return nil, "", err
			}
			return input, dataMap, err
		}
		// following line is a hack because we really dont know what an empty interface is supposed to take or yield.
		//
		scalar, init, err := m.goToGraphqlScalar(Type.Kind(), structField.Name, &htmlInfo, crumbs, nil)
		log.Println("making the interface value be a string.")
		//		input = graphql.String
		return scalar, init, err
	}
	scalar, init, err := m.goToGraphqlScalar(Type.Kind(), structField.Name, &htmlInfo, crumbs, nil)
	return scalar, init, err
}
