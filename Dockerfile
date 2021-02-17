FROM golang:1.15

RUN go get -u github.com/benvanmierloo/vitotrol2mqtt 

ENTRYPOINT ["vitotrol2mqtt", "-config", "/root/go/vitotrol2mqtt.yml"]
