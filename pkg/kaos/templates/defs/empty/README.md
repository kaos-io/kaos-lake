# {{.name}}

{{if .description}}{{.description}}

{{end}}A dataset on KAOS. Upload data with the UI or `kubecore-dataset sync <folder> --dataset {{.name}}`,
then release a version so pipelines can pick it.
