# [ipfs-search](http://ipfs-search.com)
Search engine for the [Interplanetary Filesystem](https://ipfs.io). Sniffs the DHT gossip and indexes file and directory hashes.

Metadata and contents are extracted using [ipfs-tika](https://github.com/dokterbob/ipfs-tika), searching is done using ElasticSearch 5, queueing is done using RabbitMQ. The crawler is implemented in Go, the API and frontend are built using Node.js.

## Maintainer requested
So terribly sorry, but hosting a search engine like this takes a considerable amount of resources (money _and_ TLC).

As this moment, the founders of ipfs-search had to cut hosting because of the growing resources required.</p>

If you are able to help out with either of them, mail us at info@ipfs-search.com or find us at #ipfssearch on Freenode (or #ipfs-search:chat.weho.st on Matrix).

## Roadmap
For discussing and suggesting features, look at the [project planning](https://github.com/ipfs-search/ipfs-search/projects).

## Local setup

Local installation is done using vagrant:

```bash
git clone https://github.com/ipfs-search/ipfs-search.git $GOPATH/src/github.com/ipfs-search/ipfs-search
cd $GOPATH/src/github.com/ipfs-search/ipfs-search
go get
vagrant up
```

This starts up the API on port 9615, Elasticsearch on 9200 and RabbitMQ on 15672.

Vagrant setup does not currently start up the frontend.

## Manual provisioning
```bash
$ ansible-playbook provisioning/bootstrap.yml --user root --ask-pass
$ ansible-playbook provisioning/ipfs-search.yml
```
