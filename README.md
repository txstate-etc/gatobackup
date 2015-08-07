# gatobackup
Export gato sites


**FILES:**

**~/.env file**

This file holds the **user** and **pswd** environment
variables that get sourced by the backup script.
The backup user requires read access to the whole
site.

**~/bin/backup.sh script**

This script establishes sessions to gato and pulls
the list of sites to backup. It also kicks off the
gatobackup program and pipes the list along with
passing the sessions to the gatobackup program.
Once the gatobackup program is done and no download
errors where reported, this script will then see if
any sites have been removed since the last backup.

**~/bin/gatobackup program**

This reads the list of sites to backup from the
standard input and utilizes the sessions passed
to it to start backing up the sites. It saves
all the failed backups to the **sites.failed** file.

**dump.jsp**

Backup.sh uses this script to pull a list of sites,
and gatobackup uses this script to pull a site's
meta-data to compare if there have been changes.


**TYPES OF BACKUPS:**

**Backup nodes from edit systems:**

For nodes on edits we may backup from any of the
systems. Backup goroutines will pull off the same
channel.

*Example of single channel flow:*

All threads pluck a node from a single channel.

```
                             /-->(edit1 session) backup goroutine
                            /
                           /---->(edit1 session) backup goroutine
node_list --> channel 1 --> 
                           \---->(edit2 session) backup goroutine
                            \
                             \-->(edit2 session) backup goroutine
```

**Backup nodes from public systems:**

Each node must be assigned to a public system
as UUIDs are created upon activation to the
publics and will thus be different depending
on the public it came from even though the
rendered content is the same. We can use a
hash of the site name to determine which
public goroutine session will service it.
WARNING: This now means that if one of the
public systems goes down then the hashes
of the content will not match and the node
will be backed up even though the visible
content may be the same.

*Example of split flow:*

Each of the threads utilizing a session for a
public node will pluck a node off from their
own dedicated channel. A hasing algorithm is
used to make sure that each node name always
gets assigned to a specific channel. With only
two publics we effectively emulate consistent
hashing, but should we start adding more
publics we should look at some consistent
hashing algorithms.

```
                                                 /-->(public1 session) backup goroutine
                                                /
                                 /--> channel 1 ---->(public1 session) backup goroutine
node_list --> mod_hash_func() -->
                                 \--> channel 2 ---->(public2 session) backup goroutine
                                                \
                                                 \-->(public2 session) backup goroutine
```
