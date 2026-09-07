// A small browser client; no bundler or external packages.
export async function fetchTasks() {
  const response = await fetch("/api/tasks");
  if (!response.ok) {
    throw new Error(`Task request failed: ${response.status}`);
  }
  return response.json();
}

export function renderTasks(tasks, container) {
  container.replaceChildren();
  for (const task of tasks) {
    const item = document.createElement("li");
    item.textContent = `${task.done ? "✓" : "○"} ${task.title}`;
    item.classList.toggle("done", task.done);
    container.append(item);
  }
}

const board = document.querySelector("#tasks");
const status = document.querySelector("#status");

fetchTasks()
  .then((tasks) => {
    renderTasks(tasks, board);
    const completed = tasks.filter((task) => task.done).length;
    status.textContent = `${completed} of ${tasks.length} tasks completed`;
  })
  .catch((error) => {
    status.textContent = error.message;
  });
