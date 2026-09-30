package store

import "testing"

func TestNamedSubtaskIdentityAndResidency(t *testing.T) {
	d := setupTestDB(t)
	actor := setupTestActor(t, d)
	project := setupTestContainer(t, d, actor)
	s := New(d)
	owner, e := s.Tasks.Create(actor, CreateParams{Slug: "owner", Title: "Owner", ProjectUUID: project, State: "open", Priority: 3})
	if e != nil {
		t.Fatal(e)
	}
	var before int
	if err := d.QueryRow("SELECT count(*) FROM task_seq").Scan(&before); err != nil {
		t.Fatal(err)
	}
	st, e := s.Tasks.Create(actor, CreateParams{Slug: "diagram", Title: "Diagram", SubtaskOwnerUUID: &owner.UUID, State: "open", Priority: 3})
	if e != nil {
		t.Fatal(e)
	}
	if st.ID != owner.ID+".diagram" {
		t.Fatal(st.ID)
	}
	var after int
	if err := d.QueryRow("SELECT count(*) FROM task_seq").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("subtask consumed sequence")
	}
	if _, e = s.Tasks.Create(actor, CreateParams{Slug: "diagram", Title: "duplicate", SubtaskOwnerUUID: &owner.UUID, State: "open", Priority: 3}); e == nil {
		t.Fatal("duplicate accepted")
	}
	other, e := s.Containers.Create(actor, ContainerCreateParams{Slug: "other-project", Kind: "project"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.Exec("UPDATE tasks SET project_uuid=? WHERE uuid=?", other.UUID, owner.UUID); e != nil {
		t.Fatal(e)
	}
	var resident string
	if err := d.QueryRow("SELECT project_uuid FROM tasks WHERE uuid=?", st.UUID).Scan(&resident); err != nil {
		t.Fatal(err)
	}
	if resident != other.UUID {
		t.Fatal("residency drift")
	}
	for _, q := range []string{"UPDATE tasks SET project_uuid='" + project + "' WHERE uuid='" + st.UUID + "'", "UPDATE tasks SET slug='changed' WHERE uuid='" + st.UUID + "'"} {
		if _, e = d.Exec(q); e == nil {
			t.Fatalf("guard accepted %s", q)
		}
	}
	if _, e = s.Tasks.Purge(actor, st.UUID, 0); e == nil {
		t.Fatal("subtask purge accepted")
	}
	if _, e = s.Tasks.Purge(actor, owner.UUID, 0); e == nil {
		t.Fatal("owner purge accepted")
	}
}
