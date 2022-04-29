# object

The object package may be used to transform a Go structure to a GraphQL Object.

---
* object.Marshal() "marshals" a Go Lang structure to a graphQL object.
   * There are optional struct field tags that that may be used to affect the outcome.
      * if the "description" tag is found, the Description field of the object is assigned its value.
      * if the "mor" tag is found, reflection for the field will be done using its value, which is the type of a struct. That is in contrast with the normal path of processing which is to reflect on the type of the field.
