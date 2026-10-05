package main

import "testing"

func TestMySQLDSN(t *testing.T) {
	target := DBTarget{Engine: EngineMariaDB, Host: "h", Port: 3306, User: "u", Password: "p@ss", Database: "app"}
	want := "u:p@ss@tcp(h:3306)/app?parseTime=true"
	if got := MySQLDSN(target); got != want {
		t.Errorf("MySQLDSN = %q, attendu %q", got, want)
	}
}
