# {{.name}}

{{if .description}}{{.description}}

{{end}}A YOLO object-detection dataset on KAOS.

- `images/{train,val,test}/` — the images.
- `labels/{train,val,test}/` — one `.txt` per image, same name: `<class-id> <x> <y> <w> <h>` per
  object, coordinates normalised to 0–1.
- `data.yaml` — the split folders and the {{len .classes}} class names, which pipelines read. Edit
  it in the lakeFS UI (Edit), so a change goes through a pull request.

Upload data with the UI, or `kubecore-dataset sync <folder> --dataset {{.name}}`. When the data is
ready, release a version (Release version, or `kubecore-dataset release`); pipelines offer every
version in their dataset dropdown.
