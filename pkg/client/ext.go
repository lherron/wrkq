package client

// ExtService reads and writes writer-owned extensions independently of task state.
type ExtService struct{ client *Client }

func (s ExtService) Put(params ExtPutParams) (*ExtPutResult, error) {
	principal, err := s.client.mutationPrincipal()
	if err != nil {
		return nil, err
	}
	params.PrincipalRef = principal
	var out ExtPutResult
	if err := s.client.call("wrkq.ext.put", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
func (s ExtService) Show(params ExtShowParams) (*ExtShowResult, error) {
	var out ExtShowResult
	if err := s.client.call("wrkq.ext.show", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
func (s ExtService) ListView(options ...ExtListViewParams) (*ExtListView, error) {
	var out ExtListView
	if err := s.client.call("wrkq.ext.listView", first(options), &out); err != nil {
		return nil, err
	}
	return &out, nil
}
func (s ExtService) Delete(params ExtDeleteParams) (*ExtDeleteResult, error) {
	principal, err := s.client.mutationPrincipal()
	if err != nil {
		return nil, err
	}
	params.PrincipalRef = principal
	var out ExtDeleteResult
	if err := s.client.call("wrkq.ext.delete", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
