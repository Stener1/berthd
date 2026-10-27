// oneAtATime runs the async jobs given to it in order, each after the last
// one ended, whether it worked or failed. For read-then-write saves of one
// document (lib/project-groups): two started together would read the same
// version, and the second write would undo the first.
export function oneAtATime(): <T>(job: () => Promise<T>) => Promise<T> {
  let last: Promise<unknown> = Promise.resolve();
  return (job) => {
    const run = last.then(job);
    last = run.catch(() => {});
    return run;
  };
}
