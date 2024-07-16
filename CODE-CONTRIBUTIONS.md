## Pull Requests

Code Review is an important part of any modern software development team. In this document we aim to give a set of guidelines and
_rules of thumb_.
These are not hard requirements for _every_ Pull Request, but they should help us making better decisions.

### Why do we do Pull Requests (PRs)?

We use PRs mainly for three reasons to:

1. **Share knowledge**. Although this is not the only way we can achieve it, PullRequests are
   a great helper to share the experiences, successes and failures, as well as the domain knowledge across
   users and maintainers.
2. Overall, **improve our code and architecture quality**.
3. **Help catch problems** before they reach production.

### Rules of thumb

#### Issuer

##### Avoid internal tools references

This project is an open-source project and we should avoid references to internal tools, such as JIRA, Confluence, etc.
On a general manner, any reference must be publicly accessible so anyone can understand the context of the PR.
It is accepted to create references to resources that requires a login but allow access and account creation for anybody.

If you feel the need to reference an internal tool, make sure you provide a good summary of the concerned ticket or document.
and use the word _internal_ to help identify private accesses for public readers.

##### Link to the issue

Any PR solving an existing GitHub issue must reference it. This will help to understand the context of the PR and the problem.
It will also help to track the changes and the history of the issue.

##### Use correct PR title

Any PR title should follow the next principles:

* If it's a bugfix, the PR title should start with "fix:"
* If it's a library update, the PR title should start with "chore:"

Good PR titles will help to understand what has changed over time
and will help to troubleshoot any issue in the future.
Good commit messages are particularly useful used combined
with `git log --graph --pretty=tformat:'%h %s'`.

Format:

```plaintext
<fix:> <message>
```

Example:

```plaintext
fix: solve a race condition in the HTTP client that caused the app to crash
```

##### Make small Pull Requests

Small Pull-Requests make the reviewers' lives easier and they tend to be faster to merge. There are a few options when it comes to
write small PRs:

* Separate refactors from the actual task. You can do them either before or after. But if a refactor could help to a feature
* include it in the description, so you can get the context of the origin of the it.
* Separate _vendor_ code either in another _commit_ or in another PR.
* Create small PRs against a main-task Pull-Request. Another option could be to use
* [Maiao](https://github.com/adevinta/maiao), when you have write accesses to the code repository.
* Use [feature toggles](https://martinfowler.com/articles/feature-toggles.html) if you don't want the new code to run in
  production just yet.

**ProTip™**: To evaluate whether you should do one or multiple PRs, you may ask yourself _If I revert feature "add the ability to
count to 2" should "bump testing component version" also be reverted?_

##### Explain the _why_ and write good commit messages

Explain **why** you are introducing these changes. For people with plenty of domain knowledge, it serves as a quick validation and
they will verify if the code does what you're saying it does, speeding up the review process.
For people with less domain knowledge — such as recent team members or people not familiar with the codebase - it will allow to do
a decent review.
Having people who are not that familiar with the codebase can both bring in a new perspective and it is also an efficient way to
share knowledge.

**ProTip™**: If you write the _why_ in the commit message and have a single commit in the PR, GitHub will propagate that into the
PR description upon creation.

**ProTip™**: If you write the _why_ in the commit message and use [maiao](https://github.com/adevinta/maiao), the PR
description will automatically be maintained from the commit message upon updates.
Here's an [example](https://github.com/adevinta/noe/pull/84).

There are [plenty](https://chris.beams.io/posts/git-commit/) of
[resources](https://www.freecodecamp.org/news/writing-good-commit-messages-a-practical-guide/) on how to write good commit
messages.

##### Link github issues

If you're working on a feature or a bug, it's a good practice to
[link the issue in the PR](https://docs.github.com/en/issues/tracking-your-work-with-issues/linking-a-pull-request-to-an-issue#linking-a-pull-request-to-an-issue-using-a-keyword)
description.
This will help to understand the context of the PR and the problem.
It will also help to track the changes and the history of the issue and automate the closing of the issue upon PR merge.

Example:

```plaintext
Closes: #10
```

##### Define non-goals

One important note, aside from stating what's the _goal_ of the PR might be to state what's a _non-goal_. An example could be:

> Non-Goals: Although I moved function A of place, this PR doesn't address a refactor because it would broaden the scope too
much.

In this way, we're telling our reviewers that, albeit something was considered, it should not be part of the review, since it's
out of the scope.

##### Be open to changes

Finishing our tasks feels great. It liberates dopamine and gives us a sense of accomplishment. Nevertheless, there's a reason why
we don't commit directly into the _main_ branch and why we ask our peers to review our work: we want to learn from them and we
want to listen to different opinions. Don't get too attached to your code (It could help to think in terms of our code or team's
code, rather than my code)

**Rule of thumb**: If you're not strongly opinionated and making the change is less effort than discussing it, we can apply it
directly and move on.

##### You're the shepherd of the PR

If a PR is getting too long to get reviewed, you should be the one contacting people that made comments/suggestions and are not
approving them, to make sure we move it forward.
If you don't reach consensus/commitment, you can involve the Tech Lead to help on solving the conflict.

#### Reviewer

##### Focus on the big picture

We all have opinions and software — especially if it runs on the cloud — is never in a finished state. Consider the work and
effort the PR issuer already put in,
weigh how important the task is and how it fits in the bigger picture. If there are ten flaws in a PR, perhaps only three are
critical and/or important.
Therefore, the other can be marked as _optional_ or _nit_, so we don't overwhelm the PR issuer.

##### Be clear about your requests

State clearly what you're trying to achieve: are you blocking the PR? are you nitpicking? Is a question about the code that you
don't understand? Be explicit about it.

If you think that a piece of code could be done differently, ask the following questions to yourself:

* Does it really change what we are trying to achieve here? Is it worth to tackle this _now_?
* How many other recommendations did the issuer already accepted? Remember: there's so much we can do. Everything can be improved
* Do you have an alternative? Maybe you can put it in the comment or even use
  [suggested changes](https://docs.github.com/en/github/collaborating-with-issues-and-pull-requests/incorporating-feedback-in-your-pull-request#applying-a-suggested-change),
  to lower the effort of accepting it
* Is it a big change? Maybe it makes sense to check first — face-to-face, through a VC or Slack — with the PR issuer if it makes sense
* Is it a big change? Maybe it makes sense to check first — face-to-face, through a VC or Slack — with the PR issuer if it makes
  sense

**ProTip™**: aside of Github's Approve/Request Changes/Comment system, it can be useful to point if a comment is a _nit_, so we
inform the PR issuer that we don't expect it to land in the final code.

##### Use commit suggestions

If you have a clear idea how to improve a certain piece of code, you can use
[suggested changes](https://docs.github.com/en/github/collaborating-with-issues-and-pull-requests/incorporating-feedback-in-your-pull-request#applying-a-suggested-change)
and make the issuer's life easier.

##### Not all suggestions will be merged

And that's fine. Sometimes, it's just too much. Certainly, the comment you left will be taken into account the next time.

### A word on kindness

When doing a code review, we are not seeing each other most of the time, but rather each other's code. We can’t leverage body
language or happy, in-person smiles like office-dwelling teams.
This means close attention to the language and tone of our written feedback is crucial to team happiness and morale.

Moreover, adopting an understanding tone will help your point of view to be understood, considered and achieve your goal to alter
the PR contents.

**ProTip™**: [Grammarly](https://app.grammarly.com/) has an experimental feature to show the tone perceived by a text. Abusing it
to tune your message would help you find the right tone.

#### Understand first

You're reviewing someone's work and, usually, people don’t make mistakes intentionally.
Perhaps they were having a bad day, or couldn’t come up with a better solution. Take it easy.
Ask people why they did it like that, in a polite way. Sometimes you don’t understand the trade-offs that were made.
Sit down with them if needed. We should let our ego out of the door.

##### Good 👍 ✅

> I see we're repeating this in a few similar classes instead of creating an abstraction for this use-case.
> Could you walk me through the changes so I get a better picture on this works?

##### Bad 👎 ❌

> You should use a parent class here. DRY

#### Be expressive

Sometimes it’s difficult to convey meaning through text alone. Comments can come off as terse or rude, even when the reviewer
tried to convey them in a positive and helpful light.
Sprinkling in emojis can help elevate the voice and tone for many of our team members. It helps the author read the comment in the
reviewer's voice, as if they were delivering the feedback in person.

##### Examples

> This looks good. I think we can move forward.

vs

> Wooow! This looks good 👏 I think we can move forward and put this code in production! 🚀

#### Suggest, don't command

People will be more enthusiastic about changing their work when you make polite suggestions instead of commanding actions.

Having a commanding tone is more likely cause repulsion and close the conversation from the PR author, while adopting an
understanding tone is more likely to open the conversation and get your point of view to be considered.

##### Good 👍 ✅

> Maybe it would make sense to rename the class `UserRepositoryImpl` to `RedisCachedUserRepository`, to improve readability. What
do you think?
> Perhaps I’m not understanding it correctly, but it seems that this code won’t work properly in X and Y cases. Can we review it?
If it has a bug we can fix it together!

##### Bad 👎 ❌

> Change this!
> This has a bug here! Fix it and create a test

#### Focus on the essential parts

You shouldn’t expect that all of your comments end up in the code base. That’s totally OK.
If you think that you are requesting too much changes or that some of them are not that important, please refer to them as
recommendations for future work. People will still learn and will try to improve for the next time.

It’s also possible that one or more engineers on the team have already pointed out numerous issues on a single pull request. While
the additional feedback would be technically helpful, it’s likely going to just "add to the pile"
and make the code author feel discouraged or overwhelmed.

##### Good 👍 ✅

> [Future reference] This code looks good 🚀 In future occasions, we can also use it for comprehensions instead of `map()`. I
wouldn't change it now, though.
> [Nit] We could use XX instead of YY.

##### Bad 👎 ❌

> We could use for comprehensions instead of `map()`
> Here too.
> Again, for comprehensions.
> Can you change XX for YY?

#### Make someone's day

Compliments are free to give. By leaving a sincere compliment or mention that you've learned something new you could be making
someone's day.

##### Examples

> I know we usually stub this out, but your approach of calling the actual method in this test is 💯. I feel much more confident
that we’ll catch changes to the API in the future. 😍
> Looks like it's working as-is 🚀 Great work in this PR! Thanks for taking the time to write test cases for X and Y. I think we
should keep this spirit and improve our codebase a bit every day 👏👏👏

### References

1. <https://www.ideamotive.co/blog/code-review-best-practices>
2. <https://www.atlassian.com/blog/git/written-unwritten-guide-pull-requests>
3. <https://blog.pragmaticengineer.com/pull-request-or-diff-best-practices/>
4. <https://product.voxmedia.com/2018/8/21/17549400/kindness-and-code-reviews-improving-the-way-we-give-feedback>
5. <https://blog.joaoqalves.net/post/2016/12/17/about-code-reviews/>
6. <https://dev.to/nholden/hacking-code-review-give-a-compliment-534m>
7. <https://sourcediving.com/a-practical-guide-to-small-and-easy-to-review-pull-requests-a7f04a01d5d5>
8. <https://essenceofcode.com/2019/10/29/the-art-of-small-pull-requests/>