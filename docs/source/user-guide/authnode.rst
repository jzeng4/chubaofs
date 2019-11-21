Authnode
====================

`Authnode` provides a general authentication & authorization service among `ChubaoFS` nodes. `Client`, `Master`, `Meta` and `Data` node are required to be authenticated and authorized before any resource access in another node.

Initially, each node (`Auth`, `Client`, `Master`, `Meta` or `Data` node) is launched with a secure key which is distributed by a authenticated person (for instance, cluster admin). With a valid key, a node can be identified in `Authnode` service and granted a ticket for resource access.

The overall workflow is: key creation and distribution --> ticket retrieval with key --> resource access with ticket.

Concepts
----------
- Key: a bit of secret shared data between a node and `Authnode` that asserts identity of a client.

- Ticket: a bit of data that cryptographically asserts identity and authorization (through a list of capabilities) for a service for a period of time.

- Capability: a capability is defined in the format of `node:object:action` where `node` refers to a service node (such as `auth`, `master`, `meta` or `data`), `object` refers to the resource and `action` refers to permitted activities (such as read, write and access).

.. table:: capability examples:
   :widths: auto

+-----------------------+-----------------------------------------------------+
|       capability      |                     Specification                   |
+=======================+=====================================================+
|auth:createkey:access  |have access permission for createkey in authnode     |
+-----------------------+-----------------------------------------------------+
|master:\*:\*           |have any permission for any objects in master        |
+-----------------------+-----------------------------------------------------+
|\*:\*:\*               |have any permission for any any objects in any node  |
+-----------------------+-----------------------------------------------------+

Build
---------------
Use the following commands to build client side tool for `Authnode`:

.. code-block:: bash

   $ git clone http://github.com/chubaofs/chubaofs.git
   $ cd chubaofs
   $ make build

If successful, The tool `cfs-authtool` can be found in `build/bin`.

Configurations
-------------------

Create Authnode key:

  .. code-block:: bash

    $ ./cfs-authtool authkey

  If successful, two key files can be generated under current directory ``authroot.json`` and ``authservice.json``. The first key is for key derivation (session key and client secret key) while the second key is for `Authnode` authentication.
 
  example ``authservice.json`` :

  .. code-block:: json

    {
        "id": "AuthService",
        "key": "9h/sNq4+5CUAyCnAZM927Y/gubgmSixh5hpsYQzZG20=",
        "create_ts": 1573801212,
        "role": "AuthService",
        "caps": "{\"*\"}"
    }


Edit ``authnode.json`` in `docker/conf` as following:

  - ``authRootKey``: use the value of ``key`` in ``authroot.json``
  - ``authServiceKey``: use the value of ``key`` in ``authService.json``
 
  example ``authnode.json`` :

  .. code-block:: json

    {
         "role": "authnode",
         "ip": "192.168.0.14",
         "port": "8080",
         "prof":"10088",
         "id":"1",
         "peers": "1:192.168.0.14:8080,2:192.168.0.15:8081,3:192.168.0.16:8082",
         "retainLogs":"2",
         "logDir": "/export/Logs/authnode",
         "logLevel":"info",
         "walDir":"/export/Data/authnode/raft",
         "storeDir":"/export/Data/authnode/rocksdbstore",
         "exporterPort": 9510,
         "consulAddr": "http://consul.prometheus-cfs.local",
         "clusterName":"test",
         "authServiceKey":"9h/sNq4+5CUAyCnAZM927Y/gubgmSixh5hpsYQzZG20=",
         "authRootKey":"wbpvIcHT/bLxLNZhfo5IhuNtdnw1n8kom+TimS2jpzs=",
         "enableHTTPS":false
    }

Start `Authnode` Cluster
-------------------------

In directory `docker/authnode`, run the following command to start a `Authnode` cluster.

.. code-block:: bash

  $ docker-compose up -d

Prepare
-------------------------

Create `admin` in Authnode
~~~~~~~~~~~~~~~~~~~~~~~~~~~

Get `Authnode` ticket using `authServiceKey`

  .. code-block:: bash

    $ ./cfs-authtool ticket -host=192.168.0.14:8080 -keyfile=authservice.json -output=ticket_auth.json getticket AuthService

    Parameters：

        host：will set the address (IP or URL) for `Authnode`

        keyfile：will set file path for key file which is generated above by authtool and used to get a ticket

    Output：

        output：will set ticket output file
   
    example ``ticket_auth.json`` :

    .. code-block:: json

      {
          "id": "AuthService",
          "session_key": "A9CSOGEN9CFYhnFnGwSMd4WFDBVbGmRNjaqGOhOinJE=",
          "service_id": "AuthService",
          "ticket": "RDzEiRLX1xjoUyp2TDFviE/eQzXGlPO83siNJ3QguUrtpwiHIA3PLv4edyKzZdKcEb3wikni8UxBoIJRhKzS00+nB7/9CjRToAJdT9Glhr24RyzoN8psBAk82KEDWJhnl+Y785Av3f8CkNpKv+kvNjYVnNKxs7f3x+Ze7glCPlQjyGSxqARyLisoXoXbiE6gXR1KRT44u7ENKcUjWZ2ZqKEBML9U4h0o58d3IWT+n4atWKtfaIdp6zBIqnInq0iUueRzrRlFEhzyrvi0vErw+iU8w3oPXgTi+um/PpUyto20c1NQ3XbnkWZb/1ccx4U0"
      }

Create `admin` using `Authnode` ticket

 .. code-block:: bash

  $ ./cfs-authtool api -host=192.168.0.14:8080 -ticketfile=ticket_auth.json -data=data_admin.json -output=key_admin.json AuthService createkey
   
    Parameters:
   
        ticketfile: will set file path for ticket file used to access resource
       
        data：will set file path for client ID and key data

        example  ``data_admin.json`` ：

        .. code-block:: json

          {
              "id": "MasterService",
              "role": "service",
              "caps": "{\"API\":[\"*:*:*\"]}"
          }


id: will set the client ID
role: will set the id role(either client or service)
caps: will set the capabilities of id

    Output:

        output: will set file path for secret key

Create a user in Authnode
---------------------------
Get `Authnode` ticket using `admin` key:

.. code-block:: bash

  $ ./cfs-authtool ticket -host=192.168.0.14:8080 -keyfile=key_admin.json -output=ticket_admin.json getticket AuthService


Create a new user `test` using `Authnode` ticket:

.. code-block:: bash

  $ ./cfs-authtool api -host=192.168.0.14:8080 -ticketfile=ticket_admin.json -data=data_client.json -output=key_client.json AuthService createkey

  example  ``data_client.json`` ：

  .. code-block:: json

    {
        "id": "test",
        "role": "client",
        "caps": "{\"API\":[\"master:getvol:access\"]}"
    }

- Note: `test` can access `getvol` api in `Master` node according to its capability.

Get `Master` service ticket using `test` key:

.. code-block:: bash

  $ ./cfs-authtool ticket -host=192.168.0.14:8080 -keyfile=key_client.json -output=ticket_client.json getticket MasterService


Create key for ChubaoFS cluster
--------------------------------
Create key for `Master`
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

.. code-block:: bash

  $ ./cfs-authtool api -host=192.168.0.14:8080 -ticketfile=ticket_admin.json -data=data_master.json -output=key_master.json AuthService createkey

  ``data_master`` ：

  .. code-block:: json

    {
        "id": "MasterService",
        "role": "service",
        "caps": "{\"API\":[\"*:*:*\"]}"
    }
    Note: `MasterService` is reserved for `Master`
