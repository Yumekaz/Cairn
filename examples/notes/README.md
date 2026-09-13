# Local notes journal

This real HTTP workload accepts POST bodies and persists them on `notes-data`.
GET returns the journal as plain text. It needs only BusyBox in the rootfs.
It has no authentication: use only on a trusted host/network. Published ports
may be reachable beyond localhost. A killed writer can leave `write.lock`;
inspect the process before removing that lock.

Create `notes-code` and `notes-data` using `cairn volume create`. Copy `cgi-bin/`
to the **actual host path reported by `cairn volume inspect notes-code`**, keeping
the CGI executable, then deploy `examples/notes/cairn.yaml`.

```
curl --data-binary 'first persistent note' http://127.0.0.1:8088/cgi-bin/notes
curl http://127.0.0.1:8088/cgi-bin/notes
cairn restart notes
cairn backup create notes-data
# Record the backup ID; append another note; restore the recorded ID:
cairn restore notes-data <backup-id>
```

Use `cairn backup --help` and `cairn volume --help` for installed command syntax.
Verify the first note after redeploy/restart and verify the later note is absent
after restore. Host reboot validation belongs on a disposable Linux VM, not an
interactive development workstation. Record the stack revisions and timings.
