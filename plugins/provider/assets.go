package providerassets

func Icon(id string) ([]byte, bool) {
	d, ok := Find(id)
	return d.Icon, ok && d.IconType == "image/png" && len(d.Icon) > 0
}
func SVGIcon(id string) ([]byte, bool) {
	d, ok := Find(id)
	return d.Icon, ok && d.IconType == "image/svg+xml" && len(d.Icon) > 0
}
