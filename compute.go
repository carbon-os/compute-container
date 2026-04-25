package compute_container

// ImageMount describes the on-disk paths that make up a container image.
// BaseLayer is the read-only root filesystem prepared by carbon-os/compute-image.
// Scratch is the writable overlay layer (required on Windows).
// Network is the optional HNS network name to attach (e.g. "nat"). Leave
// empty for no networking.
type ImageMount struct {
	BaseLayer string
	Scratch   string
	Network   string
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