FROM golang:1.14

ENV PATH=$PATH:/go/bin

WORKDIR /usr/src/costbot
COPY . . 
RUN ["go", "install", "-mod=vendor", "."]

CMD ["costbot"]