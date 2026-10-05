package main

import (
	"errors"
	"net"
	"strconv"

	"github.com/go-sql-driver/mysql"
)

// mariadbDialect décrit MariaDB. Le pilote MySQL ne lit pas l'URL mysql://
// de Cassiopée : MySQLDSN la traduit dans son format.
var mariadbDialect = dialect{
	engine:     EngineMariaDB,
	driver:     "mysql",
	dsn:        MySQLDSN,
	createSQL:  "CREATE TABLE IF NOT EXISTS canary_hits (id BIGINT AUTO_INCREMENT PRIMARY KEY, pod VARCHAR(255) NOT NULL, created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6))",
	insertSQL:  "INSERT INTO canary_hits (pod) VALUES (?)",
	versionSQL: "SELECT VERSION()",
	isAuthError: func(err error) bool {
		// 1045 : « Access denied for user ».
		var myErr *mysql.MySQLError
		return errors.As(err, &myErr) && myErr.Number == 1045
	},
}

// MySQLDSN traduit une cible en chaîne de connexion du pilote MySQL :
// « user:pass@tcp(hôte:port)/base?parseTime=true ». parseTime fait revenir
// les dates en time.Time plutôt qu'en octets.
func MySQLDSN(t DBTarget) string {
	c := mysql.NewConfig()
	c.User = t.User
	c.Passwd = t.Password
	c.Net = "tcp"
	c.Addr = net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	c.DBName = t.Database
	c.ParseTime = true
	return c.FormatDSN()
}
