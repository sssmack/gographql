package gographql

// Resolve Go land fields for graphQL

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"

	"github.com/graphql-go/graphql"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"gitlab.issaccorp.net/mda/vipr2/auth"
)

// Face resolves the 1st function of an interface and returns the function's value(s)
var Face = func(p graphql.ResolveParams) (interface{}, error) {
	var (
		results     []interface{}
		err         error
		structField reflect.StructField
		fieldValue  reflect.Value
	)
	structType := reflect.TypeOf(p.Source)
	structValue := reflect.ValueOf(p.Source)
	if reflect.Ptr == structValue.Kind() {
		structValue = structValue.Elem()
		structType = structValue.Type()
	}
	fieldCount := structType.NumField()
	// find the field value from the Source struct for the graphql named field
	for i := 0; i < fieldCount; i++ {
		structField = structType.Field(i)
		if p.Info.FieldName == structField.Name {
			fieldValue = structValue.Field(i)
			if fieldValue.IsZero() {
				return nil, nil
			}
			if reflect.Ptr == fieldValue.Kind() {
				fieldValue = fieldValue.Elem()
			}
			break
		}
	}
	if fieldValue.IsZero() {
		return nil, err
	}
	isList := false
	switch fieldValue.Kind() {
	case reflect.Slice:
		isList = true
		for i := 0; i < fieldValue.Len(); i++ {
			if fieldValue.Index(i).NumMethod() > 0 {
				// assume one method in the interface
				methodValue := fieldValue.Index(i).Method(0)
				values := methodValue.Call([]reflect.Value{})
				value := values[0]
				if reflect.Ptr == value.Kind() {
					value = value.Elem()
				}
				results = append(results, value.Interface())
			}
		}
	default: // single value
		if fieldValue.NumMethod() > 0 {
			// assume one method in the interface
			methodValue := fieldValue.Method(0)
			values := methodValue.Call([]reflect.Value{})
			value := values[0]
			if reflect.Ptr == value.Kind() {
				value = value.Elem()
			}
			results = append(results, value.Interface())
		}
	}
	if !isList && 0 < len(results) {
		return results[0], err
	}
	return results, err
}

var AnyTypeResolver = func(p graphql.ResolveParams) (interface{}, error) {
	var (
		m   []byte
		err error
		//result string
		re = regexp.MustCompile(`^types\.ArrayOf(.*)`)
	)
	// TODO this is only applicable for AnyType on WaitForUpdatesEx return value... will need to perform the field "Val" logic elsewhere eventually
	val := reflect.ValueOf(p.Source)
	val = val.FieldByName("Val")

	////log.Printf("Val :: Type %s - Kind %s :: Value %+v", val.Elem().Type().String(), val.Elem().Kind().String(), val.Elem().Interface())
	////log.Printf("%s", val.Elem().Type().String())
	switch val.Elem().Kind() {
	case reflect.Struct:
		submatches := re.FindStringSubmatch(val.Elem().Type().String())
		if len(submatches) > 0 { // Struct of array
			key := submatches[1]
			////log.Printf("\t%+v", val.Elem().Interface())
			array := val.Elem().FieldByName(key)
			////log.Printf("\t\t%+v (%s)", array, array.Kind().String())
			resultArray := []interface{}{}
			if array.Len() > 0 {
				for i := 0; i < array.Len(); i++ {
					////log.Printf("\t\t\t%s", array.Index(i).Kind().String())
					////log.Printf("\t\t\t%s", array.Index(i).Elem().Kind().String())
					switch array.Index(i).Kind() {
					case reflect.Struct:
						////log.Println("reflect.Struct")
						////log.Printf("%+v", array.Index(i).Interface())
						s, err := getStructure(array.Index(i).Interface())
						if err != nil {
							////log.Println("ERROR")
							return nil, err
						}
						resultArray = append(resultArray, s)
					default:
						////log.Println("default")
						resultArray = append(resultArray, array.Index(i).Interface())
					}
				}
			}
			m, err := json.Marshal(resultArray)
			if err != nil {
				////log.Println("ERROR")
				return nil, err
			}
			return string(m), err
		} else { // normal struct (i.e. match not found)
			s, err := getStructure(val.Elem().Interface())
			if err != nil {
				////log.Println("ERROR")
				return nil, err
			}
			m, err = json.Marshal(s)
			if err != nil {
				////log.Println("ERROR")
				return nil, err
			}
			return string(m), err
		}
	default:
		//m, err = json.Marshal(val.Interface())
		//result = string(val.Interface())
		return val.Interface(), err
	}
}

func getStructure(i interface{}) (interface{}, error) {
	var (
		results = map[string]interface{}{}
		//m			[]byte
		err         error
		structField reflect.StructField
		fieldValue  reflect.Value

		re = regexp.MustCompile(`^types\.ArrayOf(.*)`)
	)

	structType := reflect.TypeOf(i)
	structValue := reflect.ValueOf(i)
	if reflect.Ptr == structValue.Kind() {
		structValue = structValue.Elem()
		////log.Println(structValue.Elem())
		structType = structValue.Type()
	}

	fieldCount := structType.NumField()
	// find the field value from the Source struct for the graphql named field
	for i := 0; i < fieldCount; i++ {
		structField = structType.Field(i)
		if regexp.MustCompile(`^[a-z]`).MatchString(structField.Name) {
			continue
		}
		////log.Printf("Field: %s (fieldCount %d)", structField.Name, fieldCount)
		fieldValue = structValue.Field(i)
		if fieldValue.IsZero() {
			////log.Println("fieldValue IS ZERO")
			//continue
			//return nil, nil
			results[structField.Name] = nil
			continue
		}
		if reflect.Ptr == fieldValue.Kind() {
			fieldValue = fieldValue.Elem()
			////log.Println("fieldValue.Elem()")
		}
		if fieldValue.IsZero() {
			////log.Println("fieldValue IS ZERO")
			//return nil, err
			results[structField.Name] = nil
			continue
		}

		//isList := false
		////log.Printf("fieldValue.Kind(): %+v", fieldValue.Kind())
		////log.Printf("getStructure: %s", fieldValue.Kind().String())
		switch fieldValue.Kind() {
		case reflect.Struct:
			////log.Println("reflect.Struct")
			submatches := re.FindStringSubmatch(fieldValue.Type().String())
			if len(submatches) > 0 { // Struct of array
				////log.Println("\tStruct of array...")
				key := submatches[1]
				array := fieldValue.FieldByName(key)
				resultArray := []interface{}{}
				if array.Len() > 0 {
					for i := 0; i < array.Len(); i++ {
						switch array.Index(i).Kind() {
						case reflect.Struct:
							s, err := getStructure(array.Index(i).Elem().Interface())
							if err != nil {
								////log.Println(err)
								//return nil, err
								resultArray = append(resultArray, nil)
								continue
							}
							resultArray = append(resultArray, s)
						default:
							resultArray = append(resultArray, array.Index(i).Elem().Interface())
						}
					}
				}
				//m, err := json.Marshal(resultArray)
				//if err != nil {
				//	return nil, err
				//}
				//return string(m), err
				results[structField.Name] = resultArray
			} else { // normal struct (i.e. match not found)
				////log.Println("\tStandard struct")
				s, err := getStructure(fieldValue.Interface())
				if err != nil {
					////log.Println(err)
					results[structField.Name] = nil
					continue
					//return nil, err
				}
				//m, err = json.Marshal(s)
				//if err != nil {
				//	return nil, err
				//}
				//return string(m), err
				results[structField.Name] = s
			}
		case reflect.Slice:
			//isList = true
			////log.Println("reflect.Slice")
			val := []interface{}{}
			for i := 0; i < fieldValue.Len(); i++ {
				if fieldValue.Index(i).NumMethod() > 0 {
					////log.Printf("fieldValue.index(%d): %+v", i, fieldValue.Index(i))
					// assume one method in the interface
					methodValue := fieldValue.Index(i).Method(0)
					values := methodValue.Call([]reflect.Value{})
					value := values[0]
					if reflect.Ptr == value.Kind() {
						value = value.Elem()
					}
					////log.Printf("value: %+v", value)
					//results = append(results, value.Interface())
					val = append(val, value.Interface())
				}
			}
			results[structField.Name] = val
		default: // single value
			//if fieldValue.NumMethod() > 0 {
			//	////log.Printf("fieldValue: %+v", fieldValue)
			//	// assume one method in the interface
			//	methodValue := fieldValue.Method(0)
			//	values := methodValue.Call([]reflect.Value{})
			//	value := values[0]
			//	if reflect.Ptr == value.Kind() {
			//		value = value.Elem()
			//	}
			//	////log.Printf("value: %+v", value)
			//	results = append(results, value.Interface())
			//}
			////log.Println("DEFAULT")
			results[structField.Name] = fieldValue.Interface()
		}
	}
	////log.Printf("fieldValue.isZero(): %t", fieldValue.IsZero())

	//if !isList && 0 < len(results) {
	//	return results[0], err
	//}
	return results, err
}

// ManagedEntity returns one or more MEs as derived from the MOR(s) given at the field.
var ManagedEntity = func(p graphql.ResolveParams) (interface{}, error) {
	structType := reflect.TypeOf(p.Source)
	structValue := reflect.ValueOf(p.Source)
	if reflect.Ptr == structValue.Kind() {
		structValue = structValue.Elem()
		structType = structValue.Type()
	}

	var (
		mors   = []types.ManagedObjectReference{}
		ok     bool
		err    error
		result = []interface{}{}
		isList bool
	)
	fieldValue := structValue.FieldByName(p.Info.FieldName)
	if fieldValue.Kind() == reflect.Ptr {
		fieldValue = fieldValue.Elem()
	}
	if fieldValue.IsZero() {
		return nil, nil
	}
	// make a slice of mors if the value is a mor
	face := fieldValue.Interface()
	if reflect.Slice != fieldValue.Kind() {
		if mor, ok := face.(types.ManagedObjectReference); !ok {
			err = fmt.Errorf(
				"the type of field %s on %s is not a types.ManagedObjectReference",
				p.Info.FieldName,
				structType,
			)
			return nil, err
		} else {
			mors = append(mors, mor)
		}
	} else { // the value is already a slice of mors
		if mors, ok = face.([]types.ManagedObjectReference); !ok {
			err = fmt.Errorf(
				"the type of field %s on %s is not a []types.ManagedObjectReference",
				p.Info.FieldName,
				structType,
			)
			return nil, err
		}
		isList = true
	}

	if 0 == len(mors) {
		return result, nil
	}
	ctx := context.Background()
	client, err := auth.GetClient(p.Context)
	if nil != err {
		log.Println(err)
		return nil, err
	}
	err = property.DefaultCollector(client).Retrieve(ctx, mors, []string{}, &result)
	if nil != err {
		log.Println(err)
		return nil, err
	}
	mes := []mo.ManagedEntity{}

	for _, res := range result {
		switch v := res.(type) {
		case mo.ComputeResource:
			mes = append(mes, v.ManagedEntity)
		case mo.Datacenter:
			mes = append(mes, v.ManagedEntity)
		case mo.Datastore:
			mes = append(mes, v.ManagedEntity)
			//			if "StoragePod" == mors[0].Type {
			////log.Println("checkit", structType, p.Info.FieldName, mors)
			//			}
		case mo.DistributedVirtualSwitch:
			mes = append(mes, v.ManagedEntity)
		case mo.Folder:
			mes = append(mes, v.ManagedEntity)
		case mo.HostSystem:
		case mo.StoragePod:
			mes = append(mes, v.Folder.ManagedEntity)
		case mo.Network:
			mes = append(mes, v.ManagedEntity)
		case mo.ResourcePool:
			mes = append(mes, v.ManagedEntity)
		case mo.VirtualMachine:
			mes = append(mes, v.ManagedEntity)
		default:
			log.Printf("checkit: dont know type %T\n", v)
		}
	}

	if !isList && 0 < len(mes) {
		return mes[0], err
	}
	return mes, err
}

// Mor returns one or mor MOs by using the list of one or mor MORs that are given at the field value.
var Mor = func(p graphql.ResolveParams) (interface{}, error) {
	structType := reflect.TypeOf(p.Source)
	structValue := reflect.ValueOf(p.Source)
	if reflect.Ptr == structValue.Kind() {
		structValue = structValue.Elem()
		structType = structValue.Type()
	}

	var (
		mors   = []types.ManagedObjectReference{}
		ok     bool
		err    error
		result = []interface{}{}
		isList bool
	)
	fieldValue := structValue.FieldByName(p.Info.FieldName)
	if fieldValue.Kind() == reflect.Ptr {
		fieldValue = fieldValue.Elem()
	}
	if fieldValue.IsZero() {
		return nil, nil
	}
	// make a slice of mors if the value is a mor
	value := fieldValue.Interface()
	if reflect.Slice != fieldValue.Kind() {
		if mor, ok := value.(types.ManagedObjectReference); !ok {
			err = fmt.Errorf(
				"the type of field %s on %s is not a types.ManagedObjectReference",
				p.Info.FieldName,
				structType,
			)
			return nil, err
		} else {
			mors = []types.ManagedObjectReference{mor}
		}
	} else { // the value is already a slice of mors
		if mors, ok = value.([]types.ManagedObjectReference); !ok {
			err = fmt.Errorf(
				"the type of field %s on %s is not a []types.ManagedObjectReference",
				p.Info.FieldName,
				structType,
			)
			return nil, err
		}
		isList = true
	}

	if 0 == len(mors) {
		return result, nil
	}
	ctx := context.Background()
	client, err := auth.GetClient(p.Context)
	if nil != err {
		log.Println(err)
		return nil, err
	}
	err = property.DefaultCollector(client).Retrieve(ctx, mors, []string{}, &result)
	if nil != err {
		log.Println(err)
		return nil, err
	}
	if !isList && 0 < len(result) {
		return result[0], err
	}
	return result, err
}
