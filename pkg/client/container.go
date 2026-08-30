package client

// ContainerService exposes container lookup.
type ContainerService struct{ client *Client }

func (s ContainerService) Show(path string) (*Container, error) {
	var out Container
	if err := s.client.call("wrkq.container.show", struct {
		Path string `json:"path"`
	}{Path: path}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
