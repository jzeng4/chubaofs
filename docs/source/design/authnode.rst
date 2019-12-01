Authnode
=========

Internet and Intranet are insecure places where malicious hackers usually use tools to "sniff" sensitive information off of the network. Worse yet, client/server are not trusted to be honest about their identities. Therefore, ChubaoFS encounters some common security problems once deployed in the Network.

Security Problems
------------------

- Unauthorized parties may access the resource, such as restful API, volume information.
- Communication channel is vulnerable to `Man-in-the-middle` (MITM) attacks.

`Authnode` is the security node providing a general Authentication & Authorization framework for ChubaoFS. Besides, `Authnode` acts as a store for centralized key management of
both symmetric and asymmetric key. `Authnode` customizes the idea of authentication from `Kerberos` which is built on top of tickets. Specifically, whenever a client node (`Master`, `Meta`, `Data` or `Client` node) accesses a service, it's firstly required to show the shared secrete key for authentication in `AuthNode`. After that, `AuthenNode` would issue a time-limited ticket specifically for that service. With the purpose of authorization, capabilities are embeded in tickets.






