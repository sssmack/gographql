# gographql

package gographql translates Go struct types to Graphql types.

Why gographql?

gographql allows the declaration of the graphql schema to be declared by the Go structures as the structures are declared in the code.  gographql uses Go reflection and so the schema is created at run-time.


## Install

```shell
go get github.com/sssmack/gographql
```

**Note:** gographql uses [Go Modules](https://github.com/golang/go/wiki/Modules) to manage dependencies.

gographql handles go struct types that use their own type withing their declaration.

key/value pairs in struct tags may be used to direct features of the translation process or for providing additional data to be used in the graphql type that is to be created.

Struct tag key/values.

The value for the key named "replaceTypeWith" is a string that names a Go type that gographql will use instead of the type of the field as declared in the type struct declaration. Implement a TypeReplacer to provide a method for looking up the actual Type for the named type.

The value for the key named "description" is a string that will be assigned to the description attribute of the graphql type.

The value for the key named "required" is "true" or "false".  It only works with "ptr" kinds and will cause the graphql field to be declared NONNULL.

Structs having no fields are not translated and so will have no equivalent field in the graphql type.

Field resolver functions.

The resolver for fields of type interface produce/input a JSON document that is in the form of a string. Values that are output or input will be a string of a JSON document.

Most Go structures are composed of other structures and scalar types and so the resolution of how to input and output the data is "built-in".  For example, if a struct is composed of some ints and strings, the functions for reading and writing those datum is built into the language already.  Sometimes there is the case when the resolver needs to be custom.  To accomplish that one may implement a FieldResolverFinder for gographql to use.  FieldResolverFinder has a method that takes the name of a field type as a string, and returns its resolver function, or nil if none was found.

gographql uses viper https://github.com/spf13/viper for configuration and https://github.com/sirupsen/logrus for its logger.
The viper configuration key for setting the level of logging is "GoGraphqlLogLevel".

Example of using key values in struct tags:

```go 
 type Datastore struct {
	ManagedEntity

	Info              types.BaseDatastoreInfo        `mo:"info" required:"true" description:"Specific information about the datastore."`
	Summary           types.DatastoreSummary         `mo:"summary" required:"true" description:"Global properties of the datastore."`
	Host              []types.DatastoreHostMount     `mo:"host" required:"false" description:"Hosts attached to this datastore."`
	Vm                []types.ManagedObjectReference `mo:"vm" replaceTypeWith:"VirtualMachine" required:"false" description:"Virtual machines stored on this datastore."`
	Browser           types.ManagedObjectReference   `mo:"browser" replaceTypeWith:"HostDatastoreBrowser" required:"true" description:"DatastoreBrowser used to browse this datastore."`
	Capability        types.DatastoreCapability      `mo:"capability" required:"true" description:"Capabilities of this datastore."`
	IormConfiguration *types.StorageIORMInfo         `mo:"iormConfiguration" required:"false" description:"Configuration of storage I/O resource management for the datastore.\n  Currently we only support storage I/O resource management on VMFS volumes\n  of a datastore.\n  \n  This configuration may not be available if the datastore is not accessible\n  from any host, or if the datastore does not have VMFS volume.\n  The configuration can be modified using the method\n  ConfigureDatastoreIORM_Task\n      \nSince vSphere API 4.1, or if the datastore does not have VMFS volume.\n  The configuration can be modified using the method\n  ConfigureDatastoreIORM_Task\n      \nSince vSphere API 4.1, or if the datastore does not have VMFS volume.\n  The configuration can be modified using the method\n  ConfigureDatastoreIORM_Task\n      \nSince vSphere API 4.1, or if the datastore does not have VMFS volume.\n  The configuration can be modified using the method\n  ConfigureDatastoreIORM_Task\n      \nSince vSphere API 4.1"`
 }
```
Example of creating a graphql Output type:   

```go

	out, err := gographql.GoToGraphqlOutput(VirtualMachine{})
	if nil != err {
		return
	}
	QueryFields["VM"] = &graphql.Field{
		Type: graphql.NewList(out),
	}
```

Example of implementing a FieldResolverFinder:
```go
 type myResolverFinder struct{}

 func (mrf myResolverFinder) GetResolver(fieldType, substitutedType string) (fn graphql.FieldResolveFn) {
	switch substitutedType {
	case "ManagedEntity":
		return mor
	}
	switch fieldType {
	case "ManagedObjectReference":
		return mor
	}
	return
 }
```
Configure gographql for the FieldResolverFinder:
```go
 func Init() {
	var mrf myResolverFinder
	gographql.SetFieldResolverFinder(mrf)
 }
```
Example of implementing a TypeReplacer:
```go
 import  (
   "github.com/vmware/govmomi/vim25/mo"
   "github.com/vmware/govmomi/vim25/types"
 )
 type myTypeReplacer struct{}

 func (mtr myTypeReplacer) GetType(typeName string) (reflectType *reflect.Type) {
	if len(typeName) == 0 {
		return
	}
	Type, ok := mo.T[typeName]
	if ok {
		return &Type
	}
	c := types.ObjectContent{
		Obj: types.ManagedObjectReference{Type: typeName},
	}
	obj, err := mo.ObjectContentToType(c)
	if nil == err && obj != nil {
		Type := reflect.TypeOf(obj)
		return &Type
	}

	Type, ok = types.TypeFunc()(typeName)
	if ok {
		return &Type
	}
	return
 }
```
Configure gographql for a TypeReplacer:
```go
 func Init() {
	var mtr myTypeReplacer
	gographql.SetTypeReplacer(mtr)
 }
```


