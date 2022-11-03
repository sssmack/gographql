# gographql

package gographql translates Go struct types to Graphql types.

Why gographql?

The goals of gographql are two-fold.   

One is to remove schema definition and Go code generators from the development workflow. TGenerators take a schema definition as input and create Go structures and Go code for representing those as graphql types, etc.  The generation process creates alot of code that can take a long time to compile (minutes); causing development iterations to have a long duration. 

Second is to enable the developer to express the graphql type directly in the Go struct; as they develop.  That seems to lead to a more natural, and efficient code development experience because the developer just creates the struct, and optionally "adorns" the field tags with key/values that translate to graphql type features. Graphql and Go development can happen in one place -- in the Go code.

The idea of creating a schema definition file seems to make sense for the case when more than one programming language is being used against it.  I wonder how common is that.


**Note:** gographql uses [Go Modules](https://github.com/golang/go/wiki/Modules) to manage dependencies.

## Install

```shell
go install github.com/sssmack/gographql@latest
```
The "main" branch is a development branch.  Use the git tags to get a particular verson.

gographql handles go struct types that use their own type within their declaration (recursion).

gographql uses [viper](https://github.com/spf13/viper) for configuration and [logrus](https://github.com/sirupsen/logrus) for its logger.
The viper configuration key for setting the level of logging is "GoGraphqlLogLevel".

### Struct tag key/values

key/value pairs in struct tags may be used to direct features of the translation process or for providing additional data to be used in the graphql type that is to be created.

* The value for the key named "replaceTypeWith" is a string that names a Go type that gographql will use instead of the type of the field as declared in the struct. Implement a TypeReplacer to provide a method for looking up the actual Type for the named type.

* The value for the key named "description" is a string that will be assigned to the description attribute of the graphql type.

* The value for the key named "required" is "true" or "false".  It only works with "ptr" kinds and will cause the graphql field to be declared NONNULL.

Structs having no fields are not translated and so will have no equivalent field in the graphql type.

### Field resolver functions

The resolver for fields of type interface produce/input a JSON document that is in the form of a string. 

Most Go structures are composed of other structures and scalar types.  In most cases, everything finally resolves to a scalar type that has functions for input/output "built-in".  Sometimes there is the case when the resolver of an Output type needs to be custom.  To accomplish that, one may implement a FieldResolverFinder for gographql to use.  FieldResolverFinder has a method that takes the name of a field type as a string, and returns its resolver function, or nil if none was found.

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
	out, err := gographql.GoToGraphqlOutput(Datastore{})
	if nil != err {
		return
	}
	QueryFields["Datastore"] = &graphql.Field{
		Type: graphql.NewList(out),
	}
```

Example of implementing a FieldResolverFinder:

```go

 func mor() graphql.FieldResolveFn {}

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
Configure gographql for a FieldResolverFinder; implement an Init() in your code:

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
		thisType := reflect.TypeOf(obj)
		return &thisType
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
