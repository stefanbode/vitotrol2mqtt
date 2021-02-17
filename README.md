
# Viessmann™ Vitotrol™ device to MQTT

Typically used to send Viessmann™ Vitotrol™ boiler data to a MQTT broker

## Installation


```sh
go build -v
```

will generate a `vitotrol2mqtt` executable.


## Usage

Create a YAML file based on
[`vitotrol2mqtt.yml`](vitotrol2mqtt.yml), including your
credentials and the attributes you want to send to the MQTT broker.

Registered attributes can be found here:
https://github.com/maxatome/go-vitotrol/blob/master/attributes.go#L79
(field `Name`).

If you want an attribute that is not registered, use a name like
`NAME_0xNNNN` where `NAME` is the name of the attribute, and `NNNN` is
the hexadecimal representation of the attribute ID, for example:

```
FuelConsumption_0x108d
```

You can use [`vitotrol`](https://github.com/maxatome/go-vitotrol) +
`rget all`, `bget` or `remote_attrs` actions to discover attributes
(german language skill needed :) ).

Note that you can provide the special attribute
`ComputedSetpointTemp`. This fake attribute is computed using several
others and corresponds to the setpoint temperature (as it appears that
this value is not available in Vitotrol™ served attributes).

Once your `vitotrol2mqtt.yml` is ready, you can launch:

```sh
vitotrol2mqtt -config vitotrol2mqtt.yml
```
## Docker
Alternatively, you can run the tool in a docker container.

```sh
docker-compose build --no-cache
docker-compose up 
```

That's all.

