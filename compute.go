package compute_container

// ImageMount describes the on-disk paths that make up a container image.
// Scratch is the writable overlay layer containing layerchain.json.
// Network is the optional HNS network name to attach (e.g. "nat"). Leave
// empty for no networking.
// HyperV enables Hyper-V isolation, required when the container OS does not
// match the host OS.
type ImageMount struct {
	Scratch string
	Network string
	HyperV  bool
}

// NewContainer prepares a container from the given image paths and returns a
// handle for interacting with it. Always defer Close on the returned value.
func NewContainer(mount ImageMount) (*Container, error) {
	p, err := newPlatformContainer(mount)
	if err != nil {
		return nil, err
	}
	return &Container{p: p}, nil
}