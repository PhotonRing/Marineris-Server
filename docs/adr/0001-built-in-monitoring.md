# Built-in monitoring instead of Prometheus

Marineris samples Docker's stats API itself (every 10 s) and keeps the series in its own SQLite database: raw samples for 24 h, 1-minute averages for 7 days, 15-minute averages for 90 days. Graphs, the Status dashboard and Alerts read from there.

Prometheus with cAdvisor is the usual answer, but it is built for fleets of machines. On one internet-facing server it adds two containers, another endpoint to secure and another store to back up, for a history Marineris's graphs and fixed Alerts do not need. This follows what single-machine platforms such as Cosmos do.

If a server ever needs Prometheus, Marineris can expose a read-only `/metrics` endpoint without undoing this.
