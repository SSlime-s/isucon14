.PHONY: create-log-dir log-mysql log-nginx log-all update-go restart-mysql restart-nginx

create-log-dir:
	mkdir -p ~/webapp/log/$(shell git show --format='%h' --no-patch)

log-mysql: create-log-dir
	sudo pt-query-digest /var/log/mysql/mysql-slow.log > ~/webapp/log/$(shell git show --format='%h' --no-patch)/mysql-slow.log
	make delete-log-mysql

delete-log-mysql:
	sudo rm /var/log/mysql/mysql-slow.log
	sudo systemctl restart mysql

log-nginx: create-log-dir
	sudo cat /var/log/nginx/access.log | alp ltsv -m"/api/chair/rides/[A-Z0-9]+/status","/api/app/rides/[A-Z0-9]+/evaluation","/api/chair/[A-Z0-9]+/evaluation","/assets.*" --sort sum -r > ~/webapp/log/$(shell git show --format='%h' --no-patch)/nginx-access.log
	make delete-log-nginx

delete-log-nginx:
	sudo rm /var/log/nginx/access.log
	sudo systemctl restart nginx

push-log:
	git add ~/webapp/log/$(shell git show --format='%h' --no-patch)
	git commit -m "log $(shell git show --format='%h' --no-patch)"
	git push

log-all: log-mysql log-nginx
	make push-log

update-go:
	cd ~/webapp/go && go build -o isuride
	sudo systemctl restart isuride-go.service

restart-mysql:
	sudo systemctl restart mysql.service

restart-nginx:
	sudo systemctl restart nginx.service
