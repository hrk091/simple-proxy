# simple-proxy

Simple proxy to connect remote server without cors restriction.

```
make build
docker run -d -e TARGET_URL='...' -p 8888:8888 simple-proxy
```
