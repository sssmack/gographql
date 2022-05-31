# gographql
gographql is a Go module providing a package that translates Go struct types to Graphql types.

gographql tranlates Go structure types directly into graphql types.  Go structures become the schema definition.   
Go structure tags are used to define attributes of the graphql type, e.g. the description of the field.
Translation uses Go reflection and is quite fast.    
Most schemas are created well within a second.




## Install

```shell
go get github.com/sssmack/gographql
```
# Example of Usage
`
 out, err := gographql.GoToGraphqlOutput(datastore.MetricsCollectionDoc{})
      if nil != err {  
         log.Error(err)
         return
      }
         
      QueryFields[op] = &graphql.Field{
         Type:    graphql.NewList(out),
         Resolve: perfReport,
         Args: graphql.FieldConfigArgument{
            argName: &graphql.ArgumentConfig{
               Type:        graphql.NewList(graphql.String),
               Description: "A list of JSON path expressions.",
            },
         },    
         Description: "Reports a list of clusters that were collected on for the context.",
      }
`
