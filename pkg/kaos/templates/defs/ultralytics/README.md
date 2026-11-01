# {{.name}}

{{if .description}}{{.description}}

{{end}}An Ultralytics (YOLO format) object-detection dataset on KAOS.

- `images/{train,val,test}/` — the images.
- `labels/{train,val,test}/` — one `.txt` per image, same name: `<class-id> <x> <y> <w> <h>` per
  object, coordinates normalised to 0–1.
- `data.yaml` — the split folders and the {{len .classes}} class names, which pipelines read. Edit
  it in the lakeFS UI (Edit), so a change goes through a pull request.

## Add data and release a version

Main of a dataset changes only when a pull request is merged. In the lakeFS UI, create a branch,
upload to it and open a pull request; or use the `kubecore` command line (install it from
https://github.com/novelcore/hera-pipeline-template/tree/main/packages/kubecore):

```
kubecore login
kubecore dataset validate <folder>
kubecore dataset push <folder> {{.name}} -m "Add the first images"   # opens a pull request
kubecore dataset release {{.name}}                                  # tags main as the next version
kubecore dataset pull {{.name}}@v1 [dest]                           # download a version
```

Pipelines offer every released version in their dataset dropdown: the parameter is
`{{.name}}@<version>`.
