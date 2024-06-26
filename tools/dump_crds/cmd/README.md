# dumpcrds command

## SUMMARY

`dump` command is used to generate schemas for the Kubernetes custom resource definitions. 
The schemas are used to validate the kubechecks changes.  

## How does it work

1. Connect to the cluster you want to generate schemas from and get current version
```json
{
  "clientVersion": {
    "major": "1",
    "minor": "30",
    "gitVersion": "v1.30.1",
    "gitCommit": "6911225c3f747e1cd9d109c305436d08b668f086",
    "gitTreeState": "clean",
    "buildDate": "2024-05-14T10:50:53Z",
    "goVersion": "go1.22.2",
    "compiler": "gc",
    "platform": "darwin/arm64"
  },
  "kustomizeVersion": "v5.0.4-0.20230601165947-6ce0bf390ce3"
}
```

use the gitVersion attributes to find the current version.

2. list all CRDs in the cluster
3. create a directory `<version>` for the matching version (e.g. v1.30.1 -> v1.30.0)
4. for each CRD, get the `version.schema.openAPIV3Schema` and save it to a file
5. file name is in the format `crd-<crd-name>-<version>.json` 

## How to add a new command

1. create a new cobra.Command file in the `dumpcrd` directory.
2. make sure the file attaches itself to the rootCmd. (e.g. `rootCmd.AddCommand(newCmd)`)
