Authnode
====================

`Authnode` provides a general authentication & authorization service among `ChubaoFS` nodes. `Client`, `Master`, `Meta` and `Data` node are required to be authenticated and authorized before any resource access in another node.

Initially, each node (`Auth`, `Client`, `Master`, `Meta` or `Data` node) is launched with a secure key which is distributed by a authenticated person (for instance, cluster admin). With a valid key, a node can be identified in `Authnode` service and granted a ticket for resource access.

The overall workflow is: key creation and distribution --> ticket retrieval with key --> resource access with ticket.

Concepts
----------
- Key: a bit of secret shared data between a node and `Authnode` that asserts identity of a client.

- Ticket: a bit of data that cryptographically asserts identity and authorization (through a list of capabilities) for a service for a period of time.

- Capability: a capability is defined in the format of `node:object:action` where `node` refers to a service node (such as `auth`, `master`, `meta` or `data`), `object` refers to the resource and `action` refers to permitted activities (such as read, write and access). See examples below.

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

Description
~~~~~~~~~~~~~~~~~~~~
cfs-authtool is a utility to create, view, and modify a Ceph keyring file. A keyring file stores one or more Ceph authentication keys and possibly an associated capability specification. Each key is associated with an entity name, of the form {client,mon,mds,osd}.name.

WARNING Ceph provides authentication and protection against man-in-the-middle attacks once secret keys are in place. However, data over the wire is not encrypted, which may include the messages used to configure said keys. The system is primarily intended to be used in trusted environments.


Synopsis
~~~~~~~~~~~~~~~~~~~~
cfs-authtool keyringfile [ -l | –list ] [ -p | –print-key ] [ -C | –create-keyring ] [ -g | –gen-key ] [ –gen-print-key ] [ –import-keyring otherkeyringfile ] [ -n | –name entityname ] [ -u | –set-uid auid ] [ -a | –add-key base64_key ] [ –cap subsystem capability ] [ –caps capfile ]



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


Create key for ChubaoFS cluster
--------------------------------

Get `Authnode` ticket using `admin` key:

.. code-block:: bash

  $ ./cfs-authtool ticket -host=192.168.0.14:8080 -keyfile=key_admin.json -output=ticket_admin.json getticket AuthService


Create key for Master
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
    `MasterService` is reserved for `Master` node. from the output file `key_master.json`, copy `key` and its value to `master.json` and rename `key` as 'masterServiceKey'.

  Create key for Client
  ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

  .. code-block:: bash

    $ ./cfs-authtool api -host=192.168.0.14:8080 -ticketfile=ticket_admin.json -data=data_client.json -output=key_client.json AuthService createkey

  ``data_client`` :
    
  .. code-block:: json
  
    {
        "id": "ltptest",
        "role": "client",
        "caps": "{\"API\":[\"*:*:*\"]}"
    }
  Copy `key` and its value to `client.json` and rename `key` as `clientKey`

  ``client.json`` ：
  
  .. code-block:: json
  
    {
        "masterAddr": "192.168.0.11:17010,192.168.0.12:17010,192.168.0.13:17010",
        "mountPoint": "/cfs/mnt",
        "volName": "ltptest",
        "owner": "ltptest",
        "logDir": "/cfs/log",
        "logLevel": "info",
        "consulAddr": "http://192.168.0.100:8500",
        "exporterPort": 9500,
        "profPort": "17410",
        "authenticate": true,
        "ticketHost": "192.168.0.14:8080,192.168.0.15:8081,192.168.0.16:8082",
        "clientKey": "jgBGSNQp6mLbu7snU8wKIdEkytzl+pO5/OZOJPpIgH4=",
        "enableHTTPS": "false"
    }
    
  Parameter：
  
      authenticate: will enable authentication flow if set true.
      
      ticketHost: will set the IP/URL of `Authnode` cluster.
      
      clientKey: will set the key generated by `Authnode`
      
      enableHTTPS: will enable HTTPS if set true.


Start ChubaoFS cluster
-----------------------  

  .. code-block:: bash
  
    $ docker/run_docker.sh -r -d /data/disk

  在客户端的启动过程中，会先使用clientKey从authnode节点处获取访问Master节点的ticket，再使用ticket访问Master API。因此，只有被受权的客户端才能成功启动并挂载。

