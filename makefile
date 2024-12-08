.PHONY: create-log-dir log-mysql log-nginx log-all

create-log-dir:
	mkdir -p ~/webapp/log/$(HASH)

log-mysql: create-log-dir
	sudo pt-query-digest /var/log/mysql/mysql-slow.log > ~/webapp/log/$(HASH)/mysql-slow.log
	sudo rm /var/log/mysql/mysql-slow.log
	sudo systemctl restart mysql

log-nginx: create-log-dir
	sudo cat /var/log/nginx/access.log | alp ltsv -m"/api/chair/rides/[A-Z0-9]+/status","/api/app/rides/[A-Z0-9]+/evaluation","/api/chair/[A-Z0-9]+/evaluation","/assets.*" --sort sum -r > ~/webapp/log/$(HASH)/nginx-access.log
	sudo rm /var/log/nginx/access.log
	sudo systemctl restart nginx

log-all: log-mysql log-nginx
